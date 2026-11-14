-- +goose Up
-- The outbox (ADR-0031): one row per event and subscriber, claimed with a
-- lease and marked sent, failed or dead. Unlike events and the ledger it is
-- working state, not memory: its rows change, but only through the bounded
-- transitions the trigger below allows, for every role.

-- Composite key target, so that a row of the outbox must live in the space of
-- its event. It adds no data rule: seq is already unique on its own.
ALTER TABLE kernel.events ADD CONSTRAINT events_seq_space_key UNIQUE (seq, space_id);

CREATE TABLE kernel.outbox (
  event_seq bigint NOT NULL,
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  subscriber text NOT NULL CONSTRAINT outbox_subscriber_check
    CHECK (subscriber IN ('webhooks', 'n8n', 'ai', 'internal')),
  status text NOT NULL DEFAULT 'pending' CONSTRAINT outbox_status_check
    CHECK (status IN ('pending', 'sent', 'failed', 'dead')),
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL,
  last_error text CHECK (last_error IS NULL OR (btrim(last_error) <> '' AND char_length(last_error) <= 2000)),
  delivered_at timestamptz,
  -- A claim outlives the transaction that made it: the token names the holder
  -- and the lease says until when. Both are set or both are null.
  claim_token uuid,
  claimed_until timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (event_seq, subscriber),
  CONSTRAINT outbox_event_fkey FOREIGN KEY (event_seq, space_id)
    REFERENCES kernel.events (seq, space_id),
  CONSTRAINT outbox_claim_check CHECK ((claim_token IS NULL) = (claimed_until IS NULL)),
  -- The limit of 5 attempts lives here and in model.OutboxMaxAttempts: a row is
  -- dead exactly when it has failed 5 times, so no role can skip or evade it.
  CONSTRAINT outbox_state_check CHECK (
    (status = 'pending' AND attempts = 0 AND last_error IS NULL AND delivered_at IS NULL)
    OR (status = 'failed' AND attempts BETWEEN 1 AND 4 AND last_error IS NOT NULL AND delivered_at IS NULL)
    OR (status = 'sent' AND attempts BETWEEN 0 AND 4 AND delivered_at IS NOT NULL AND claim_token IS NULL)
    OR (status = 'dead' AND attempts = 5 AND last_error IS NOT NULL AND delivered_at IS NULL AND claim_token IS NULL))
);

CREATE INDEX outbox_due_idx ON kernel.outbox (space_id, subscriber, next_attempt_at, event_seq)
  WHERE status IN ('pending', 'failed') AND claim_token IS NULL;
CREATE INDEX outbox_lease_idx ON kernel.outbox (space_id, subscriber, claimed_until)
  WHERE claim_token IS NOT NULL;

ALTER TABLE kernel.outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.outbox FORCE ROW LEVEL SECURITY;

-- Per-command policies and none for DELETE: removal is refused for lack of
-- privilege, not silently matched to no rows.
CREATE POLICY outbox_select ON kernel.outbox FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY outbox_insert ON kernel.outbox FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY outbox_update ON kernel.outbox FOR UPDATE
  USING (space_id = (SELECT kernel.current_space_id()))
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));

-- Grants by column. A new row can only start as pending: status, attempts, the
-- claim and the delivery are not insertable. UPDATE leaves out the identity of
-- the row. No DELETE, TRUNCATE, REFERENCES or TRIGGER.
GRANT SELECT ON kernel.outbox TO nexusthrive_api;
GRANT INSERT (event_seq, space_id, subscriber, next_attempt_at) ON kernel.outbox TO nexusthrive_api;
GRANT UPDATE (status, attempts, next_attempt_at, last_error, delivered_at, claim_token, claimed_until)
  ON kernel.outbox TO nexusthrive_api;

-- Removal is refused for everyone, also through TRUNCATE ... CASCADE from the
-- tables the outbox references. Retention will come with its own migration.
-- +goose StatementBegin
CREATE FUNCTION kernel.reject_outbox_removal() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  RAISE EXCEPTION 'outbox rows are never removed' USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER outbox_no_delete BEFORE DELETE ON kernel.outbox
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_outbox_removal();
CREATE TRIGGER outbox_no_truncate BEFORE TRUNCATE ON kernel.outbox
  FOR EACH STATEMENT EXECUTE FUNCTION kernel.reject_outbox_removal();

-- The transitions of a row, enforced for every role including the owner:
--  1. The identity of the row never changes.
--  2. sent and dead are final.
--  3. A free row can only be claimed: the token is set and nothing else moves.
--  4. A claimed row leaves the claim and only two ways: sent (attempts, error
--     and schedule untouched) or failed or dead (exactly one more attempt and
--     a next_attempt_at that does not go back). Re-claiming is not a way out.
-- The trigger checks the shape and not the time: the database cannot see the
-- clock the kernel injects, so delivered_at and next_attempt_at are set in Go.
-- +goose StatementBegin
CREATE FUNCTION kernel.guard_outbox_change() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  IF NEW.event_seq IS DISTINCT FROM OLD.event_seq
     OR NEW.space_id IS DISTINCT FROM OLD.space_id
     OR NEW.subscriber IS DISTINCT FROM OLD.subscriber
     OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'the identity of an outbox row cannot change' USING ERRCODE = 'restrict_violation';
  END IF;

  IF OLD.status IN ('sent', 'dead') THEN
    RAISE EXCEPTION 'an outbox row that is % cannot change', OLD.status USING ERRCODE = 'restrict_violation';
  END IF;

  IF OLD.claim_token IS NULL THEN
    IF NEW.claim_token IS NULL
       OR NEW.status IS DISTINCT FROM OLD.status
       OR NEW.attempts IS DISTINCT FROM OLD.attempts
       OR NEW.next_attempt_at IS DISTINCT FROM OLD.next_attempt_at
       OR NEW.last_error IS DISTINCT FROM OLD.last_error
       OR NEW.delivered_at IS DISTINCT FROM OLD.delivered_at THEN
      RAISE EXCEPTION 'an unclaimed outbox row can only be claimed' USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
  END IF;

  IF NEW.claim_token IS NOT NULL THEN
    RAISE EXCEPTION 'a claimed outbox row cannot be claimed again' USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.status = 'sent' THEN
    IF NEW.attempts IS DISTINCT FROM OLD.attempts
       OR NEW.last_error IS DISTINCT FROM OLD.last_error
       OR NEW.next_attempt_at IS DISTINCT FROM OLD.next_attempt_at THEN
      RAISE EXCEPTION 'sending an outbox row changes only its delivery' USING ERRCODE = 'restrict_violation';
    END IF;
  ELSIF NEW.status IN ('failed', 'dead') THEN
    IF NEW.attempts IS DISTINCT FROM OLD.attempts + 1
       OR NEW.next_attempt_at < OLD.next_attempt_at
       OR NEW.delivered_at IS DISTINCT FROM OLD.delivered_at THEN
      RAISE EXCEPTION 'failing an outbox row adds exactly one attempt and never moves its schedule back' USING ERRCODE = 'restrict_violation';
    END IF;
  ELSE
    RAISE EXCEPTION 'a claimed outbox row can only be sent, failed or dead' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER outbox_guard_change BEFORE UPDATE ON kernel.outbox
  FOR EACH ROW EXECUTE FUNCTION kernel.guard_outbox_change();

-- +goose Down
DROP TABLE kernel.outbox;
DROP FUNCTION kernel.guard_outbox_change();
DROP FUNCTION kernel.reject_outbox_removal();
ALTER TABLE kernel.events DROP CONSTRAINT events_seq_space_key;
