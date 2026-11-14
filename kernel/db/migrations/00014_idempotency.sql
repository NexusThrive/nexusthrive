-- +goose Up
-- Idempotency records (ADR-0026). One append-only row per execution of an
-- idempotent operation: the key of the caller, the fingerprint of the request
-- and the response of the first execution. A record never changes and is never
-- deleted. When one expires, the next execution of the same key inserts the
-- next generation, and the greatest generation is the one in force. The
-- triggers hold for every writer, so no operation can forget a rule.

CREATE TABLE kernel.idempotency_records (
  -- The id is the id of the execution (a UUID v7 made by the engine).
  id uuid NOT NULL CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  key text NOT NULL CONSTRAINT idempotency_records_key_check CHECK (key ~ '^[A-Za-z0-9._:-]{1,128}$'),
  -- Stamped by a trigger: no role chooses it.
  generation integer NOT NULL CONSTRAINT idempotency_records_generation_check CHECK (generation >= 1),
  operation text NOT NULL CONSTRAINT idempotency_records_operation_check
    CHECK (char_length(operation) <= 128 AND operation ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
  actor_participant_id uuid NOT NULL,
  -- SHA-256 of the canonical request; the body itself is never stored.
  request_hash bytea NOT NULL CONSTRAINT idempotency_records_hash_size CHECK (octet_length(request_hash) = 32),
  response jsonb NOT NULL CONSTRAINT idempotency_records_response_check
    CHECK (jsonb_typeof(response) = 'object' AND octet_length(response::text) <= 65536),
  -- Written by the engine with its injectable clock. A trigger keeps it inside
  -- the window from 5 minutes before now() up to now().
  created_at timestamptz NOT NULL,
  -- Stamped by a trigger: no role chooses it.
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (space_id, key, generation),
  CONSTRAINT idempotency_records_id_space_key UNIQUE (id, space_id),
  CONSTRAINT idempotency_records_ttl CHECK (expires_at = created_at + interval '24 hours'),
  -- Space travels in the key, so a record cannot name an actor of another space.
  CONSTRAINT idempotency_records_actor_fkey FOREIGN KEY (actor_participant_id, space_id)
    REFERENCES kernel.participants (id, space_id)
);

ALTER TABLE kernel.idempotency_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.idempotency_records FORCE ROW LEVEL SECURITY;

-- Only SELECT and INSERT policies: a record is never updated or deleted, and the
-- API role has no privilege to try.
CREATE POLICY idempotency_records_select ON kernel.idempotency_records FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY idempotency_records_insert ON kernel.idempotency_records FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));

-- Grants by column. generation and expires_at have no grant: the trigger stamps
-- them. No UPDATE, DELETE, TRUNCATE, REFERENCES or TRIGGER.
GRANT SELECT ON kernel.idempotency_records TO nexusthrive_api;
GRANT INSERT (id, space_id, key, operation, actor_participant_id, request_hash, response, created_at)
  ON kernel.idempotency_records TO nexusthrive_api;

-- The advisory lock of a (space, key) pair, held until the transaction ends. The
-- engine takes it before it looks for a record, and the insert trigger takes it
-- again (the lock is reentrant), so there is one definition of the lock. A hash
-- collision between two keys only makes one wait for the other, it never
-- decides anything.
-- +goose StatementBegin
CREATE FUNCTION kernel.idempotency_lock(lock_key text) RETURNS void
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  current_space uuid := kernel.current_space_id();
BEGIN
  IF current_space IS NULL THEN
    RAISE EXCEPTION 'the transaction has no current space to lock an idempotency key in'
      USING ERRCODE = 'not_null_violation';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended(current_space::text || '/' || lock_key, 0));
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION kernel.idempotency_lock(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION kernel.idempotency_lock(text) TO nexusthrive_api;

-- Guard of every new record, for every writer, the owner of the table included.
-- It refuses a record outside the current space and one born in the future of
-- the clock of the database, takes the lock of the key, refuses a key that has a
-- record in force and stamps the generation and the expiry.
-- +goose StatementBegin
CREATE FUNCTION kernel.idempotency_records_guard_insert() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  prior_generation integer;
  prior_expires timestamptz;
BEGIN
  IF NEW.space_id IS DISTINCT FROM kernel.current_space_id() THEN
    RAISE EXCEPTION 'an idempotency record is written in the current space only' USING ERRCODE = 'restrict_violation';
  END IF;
  -- The window of the injectable clock: never ahead of the database, and not
  -- more than 5 minutes behind it (model.IdempotencyClockTolerance). A record
  -- born older than that would start with less than its 24 hours, or already
  -- expired, and a repetition would run the handler again.
  IF NEW.created_at IS NULL OR NEW.created_at > now() THEN
    RAISE EXCEPTION 'an idempotency record cannot be born in the future of the database clock'
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.created_at < now() - interval '5 minutes' THEN
    RAISE EXCEPTION 'an idempotency record cannot be born more than 5 minutes behind the database clock'
      USING ERRCODE = 'restrict_violation';
  END IF;
  PERFORM kernel.idempotency_lock(NEW.key);
  SELECT r.generation, r.expires_at INTO prior_generation, prior_expires
  FROM kernel.idempotency_records r
  WHERE r.space_id = NEW.space_id AND r.key = NEW.key
  ORDER BY r.generation DESC
  LIMIT 1;
  IF FOUND THEN
    IF prior_expires > now() THEN
      RAISE EXCEPTION 'the idempotency key has a record in force'
        USING ERRCODE = 'unique_violation', CONSTRAINT = 'idempotency_records_key_in_force';
    END IF;
    NEW.generation := prior_generation + 1;
  ELSE
    NEW.generation := 1;
  END IF;
  NEW.expires_at := NEW.created_at + interval '24 hours';
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER idempotency_records_guard_insert BEFORE INSERT ON kernel.idempotency_records
  FOR EACH ROW EXECUTE FUNCTION kernel.idempotency_records_guard_insert();

-- Records are never changed, deleted or truncated, whoever asks (ADR-0028).
-- TRUNCATE ... CASCADE from a parent table fires the statement trigger too.
-- +goose StatementBegin
CREATE FUNCTION kernel.reject_idempotency_change() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  RAISE EXCEPTION 'idempotency records cannot be changed, deleted or truncated' USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER idempotency_records_no_update BEFORE UPDATE ON kernel.idempotency_records
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_idempotency_change();
CREATE TRIGGER idempotency_records_no_delete BEFORE DELETE ON kernel.idempotency_records
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_idempotency_change();
CREATE TRIGGER idempotency_records_no_truncate BEFORE TRUNCATE ON kernel.idempotency_records
  FOR EACH STATEMENT EXECUTE FUNCTION kernel.reject_idempotency_change();

-- +goose Down
DROP TABLE kernel.idempotency_records;
DROP FUNCTION kernel.reject_idempotency_change();
DROP FUNCTION kernel.idempotency_records_guard_insert();
DROP FUNCTION kernel.idempotency_lock(text);
