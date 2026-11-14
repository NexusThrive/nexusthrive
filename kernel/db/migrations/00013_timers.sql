-- +goose Up
-- Timers and the expiry of commitments (ADR-0034). Every commitment has one
-- timer that fires at its due time. A commitment becomes expired only through
-- that timer: the timer fires when the clock of the database says it is due,
-- the firing cascades to the commitment, and the event that records it is
-- required by a check at commit. The triggers hold for every writer, so no
-- operation can forget to create a timer or forge an expiry.

CREATE TABLE kernel.timers (
  -- The id of the timer is the id of its commitment (already a UUID v7): a
  -- trigger cannot generate one, and a commitment has a single expiry timer.
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  commitment_id uuid NOT NULL,
  fire_at timestamptz NOT NULL,
  status text NOT NULL DEFAULT 'scheduled'
    CONSTRAINT timers_status_check CHECK (status IN ('scheduled', 'fired', 'canceled')),
  -- Stamped by a trigger with the clock of the database; no role chooses it.
  fired_at timestamptz,
  -- Stamped by a trigger: no role chooses it.
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT timers_fired_coherent CHECK ((status = 'fired') = (fired_at IS NOT NULL)),
  CONSTRAINT timers_fired_after_fire_at CHECK (fired_at IS NULL OR fired_at >= fire_at),
  -- Space travels in the key, so a timer cannot point at a commitment of another space.
  CONSTRAINT timers_commitment_fkey FOREIGN KEY (commitment_id, space_id)
    REFERENCES kernel.commitments (id, space_id)
);

-- A commitment has at most one scheduled timer, even for two writers at once.
CREATE UNIQUE INDEX timers_one_scheduled_key ON kernel.timers (commitment_id) WHERE status = 'scheduled';
-- The scan of a firing round: the due timers of one space, oldest first.
CREATE INDEX timers_due_idx ON kernel.timers (space_id, fire_at, id) WHERE status = 'scheduled';
-- The check at commit looks up the event of an expiry by its subject.
CREATE INDEX events_expired_subject_idx ON kernel.events (space_id, subject_ref) WHERE type = 'kernel.commitment.expired';

-- Commitments that already exist get the timer they would have had. It is done
-- before row level security is forced so that the migration role is not hidden
-- from the rows, and before the triggers exist.
INSERT INTO kernel.timers (id, space_id, commitment_id, fire_at)
SELECT c.id, c.space_id, c.id, c.due_at FROM kernel.commitments c WHERE c.status = 'open';

ALTER TABLE kernel.timers ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.timers FORCE ROW LEVEL SECURITY;

-- No delete policy: a timer is never deleted, and the API role has no privilege to try.
CREATE POLICY timers_select ON kernel.timers FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY timers_insert ON kernel.timers FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY timers_update ON kernel.timers FOR UPDATE
  USING (space_id = (SELECT kernel.current_space_id()))
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));

-- Grants by column. The triggers run with the rights of the writer (there is no
-- SECURITY DEFINER), so the API role needs the columns the triggers write: the
-- insert of the creation trigger, the fire time of a reschedule and the status
-- of a firing or a cancellation. The guards accept no value but the derived one
-- and never let a caller write fired_at or created_at, which have no grant.
GRANT SELECT ON kernel.timers TO nexusthrive_api;
GRANT INSERT (id, space_id, commitment_id, fire_at) ON kernel.timers TO nexusthrive_api;
GRANT UPDATE (status, fire_at) ON kernel.timers TO nexusthrive_api;
-- The cascade of a firing and the move of a due date run as the writer too.
-- Both columns are guarded by guard_commitment_update below.
GRANT UPDATE (due_at, status) ON kernel.commitments TO nexusthrive_api;

-- Guard of every new timer. A timer is born scheduled, for an open commitment
-- of its own space, at the due time of that commitment. The commitment is locked
-- first, as everywhere in the kernel (commitment, then timer).
-- +goose StatementBegin
CREATE FUNCTION kernel.timers_guard_insert() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  commitment_status text;
  commitment_due timestamptz;
BEGIN
  IF NEW.status IS DISTINCT FROM 'scheduled' OR NEW.fired_at IS NOT NULL THEN
    RAISE EXCEPTION 'a timer is created scheduled and not fired' USING ERRCODE = 'restrict_violation';
  END IF;
  SELECT c.status, c.due_at INTO commitment_status, commitment_due
  FROM kernel.commitments c
  WHERE c.id = NEW.commitment_id AND c.space_id = NEW.space_id
  FOR NO KEY UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'the commitment of a timer must exist in the space of the timer'
      USING ERRCODE = 'foreign_key_violation', CONSTRAINT = 'timers_commitment_fkey';
  END IF;
  IF commitment_status <> 'open' THEN
    RAISE EXCEPTION 'only an open commitment has a scheduled timer' USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.fire_at IS DISTINCT FROM commitment_due THEN
    RAISE EXCEPTION 'a timer fires at the due time of its commitment' USING ERRCODE = 'restrict_violation';
  END IF;
  NEW.created_at := now();
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER timers_guard_insert BEFORE INSERT ON kernel.timers
  FOR EACH ROW EXECUTE FUNCTION kernel.timers_guard_insert();

-- Guard of every change to a timer. A fired or canceled timer never changes.
-- A scheduled one admits exactly one effect per statement, decided against its
-- commitment, which is locked first:
--  fire_at          only while the commitment is open and only to its due time.
--  status 'fired'   only while the commitment is open and the fire time has
--                   come by the clock of the database; fired_at is stamped here.
--  status 'canceled' only once the commitment is no longer open.
-- The owner of the table is held to the same rules.
-- +goose StatementBegin
CREATE FUNCTION kernel.timers_guard_update() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  effects integer := 0;
  commitment_status text;
  commitment_due timestamptz;
BEGIN
  IF OLD.status <> 'scheduled' THEN
    RAISE EXCEPTION 'a timer that has fired or been canceled cannot change' USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.space_id IS DISTINCT FROM OLD.space_id
     OR NEW.commitment_id IS DISTINCT FROM OLD.commitment_id OR NEW.created_at IS DISTINCT FROM OLD.created_at
     OR NEW.fired_at IS DISTINCT FROM OLD.fired_at THEN
    RAISE EXCEPTION 'only the status and the fire time of a scheduled timer can change'
      USING ERRCODE = 'restrict_violation';
  END IF;

  IF NEW.status IS DISTINCT FROM OLD.status THEN
    effects := effects + 1;
  END IF;
  IF NEW.fire_at IS DISTINCT FROM OLD.fire_at THEN
    effects := effects + 1;
  END IF;
  IF effects = 0 THEN
    RETURN NEW;
  END IF;
  IF effects > 1 THEN
    RAISE EXCEPTION 'a timer changes in one way at a time' USING ERRCODE = 'restrict_violation';
  END IF;

  SELECT c.status, c.due_at INTO commitment_status, commitment_due
  FROM kernel.commitments c
  WHERE c.id = OLD.commitment_id AND c.space_id = OLD.space_id
  FOR NO KEY UPDATE;
  -- A commitment that row level security hides is refused, not assumed.
  IF NOT FOUND THEN
    RAISE EXCEPTION 'the commitment of the timer is not visible' USING ERRCODE = 'restrict_violation';
  END IF;

  IF NEW.fire_at IS DISTINCT FROM OLD.fire_at THEN
    IF commitment_status <> 'open' OR NEW.fire_at IS DISTINCT FROM commitment_due THEN
      RAISE EXCEPTION 'a timer fires at the due time of its open commitment' USING ERRCODE = 'restrict_violation';
    END IF;
  ELSIF NEW.status = 'fired' THEN
    IF commitment_status <> 'open' OR OLD.fire_at > now() THEN
      RAISE EXCEPTION 'a timer fires only for an open commitment, once its time has come'
        USING ERRCODE = 'restrict_violation';
    END IF;
    NEW.fired_at := now();
  ELSIF NEW.status = 'canceled' THEN
    IF commitment_status = 'open' THEN
      RAISE EXCEPTION 'a timer is canceled only when its commitment is closed' USING ERRCODE = 'restrict_violation';
    END IF;
  ELSE
    RAISE EXCEPTION 'a timer cannot return to scheduled' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER timers_guard_update BEFORE UPDATE ON kernel.timers
  FOR EACH ROW EXECUTE FUNCTION kernel.timers_guard_update();

-- A timer that fires expires its commitment. The guard of the commitment
-- accepts the change only because this timer fired in this very transaction.
-- +goose StatementBegin
CREATE FUNCTION kernel.timers_expire_commitment() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  UPDATE kernel.commitments c SET status = 'expired'
  WHERE c.id = NEW.commitment_id AND c.space_id = NEW.space_id AND c.status = 'open';
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER timers_expire_commitment AFTER UPDATE ON kernel.timers
  FOR EACH ROW WHEN (OLD.status = 'scheduled' AND NEW.status = 'fired')
  EXECUTE FUNCTION kernel.timers_expire_commitment();

-- Every new commitment gets its timer, whoever writes it.
-- +goose StatementBegin
CREATE FUNCTION kernel.commitments_timer_create() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  INSERT INTO kernel.timers (id, space_id, commitment_id, fire_at)
  VALUES (NEW.id, NEW.space_id, NEW.id, NEW.due_at);
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER commitments_timer_create AFTER INSERT ON kernel.commitments
  FOR EACH ROW EXECUTE FUNCTION kernel.commitments_timer_create();

-- The timer follows its commitment: a new due date moves it, and closing the
-- commitment cancels it. After an expiry no scheduled timer is left, so the
-- cascade ends here.
-- +goose StatementBegin
CREATE FUNCTION kernel.commitments_timer_sync() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  IF NEW.status <> 'open' THEN
    UPDATE kernel.timers t SET status = 'canceled'
    WHERE t.commitment_id = NEW.id AND t.space_id = NEW.space_id AND t.status = 'scheduled';
  ELSIF NEW.due_at IS DISTINCT FROM OLD.due_at THEN
    UPDATE kernel.timers t SET fire_at = NEW.due_at
    WHERE t.commitment_id = NEW.id AND t.space_id = NEW.space_id AND t.status = 'scheduled';
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER commitments_timer_sync AFTER UPDATE ON kernel.commitments
  FOR EACH ROW WHEN (OLD.due_at IS DISTINCT FROM NEW.due_at OR OLD.status IS DISTINCT FROM NEW.status)
  EXECUTE FUNCTION kernel.commitments_timer_sync();

-- Guard of every change to a commitment, replacing the one of ADR-0033. A
-- closed commitment never changes. An open one admits exactly one effect per
-- statement, and the kernel derives the state that follows from it:
--  fulfilled_amount  must equal the sum of the fulfillments; reaching the
--                    amount makes the commitment fulfilled.
--  canceled_reason   from null to a reason cancels the commitment.
--  due_at            moves the due date, only strictly after the present: a
--                    date that has passed would let a writer force an expiry.
--  status 'expired'  closes it as expired, only when its due date has passed
--                    and its timer fired in this same transaction, at a time
--                    that had come. The kernel never takes the word of a caller.
-- Every closing effect stamps closed_at with the clock of the database.
-- Whatever else a caller writes is refused, the owner of the table included.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION kernel.guard_commitment_update() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  effects integer := 0;
  fulfilled numeric;
BEGIN
  IF OLD.status <> 'open' THEN
    RAISE EXCEPTION 'a closed commitment cannot change' USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.space_id IS DISTINCT FROM OLD.space_id
     OR NEW.kind IS DISTINCT FROM OLD.kind
     OR NEW.debtor_participant_id IS DISTINCT FROM OLD.debtor_participant_id
     OR NEW.creditor_participant_id IS DISTINCT FROM OLD.creditor_participant_id
     OR NEW.resource_id IS DISTINCT FROM OLD.resource_id OR NEW.amount IS DISTINCT FROM OLD.amount
     OR NEW.starts_at IS DISTINCT FROM OLD.starts_at
     OR NEW.origin_ref IS DISTINCT FROM OLD.origin_ref OR NEW.created_at IS DISTINCT FROM OLD.created_at
     OR NEW.created_by_participant_id IS DISTINCT FROM OLD.created_by_participant_id
     OR NEW.closed_at IS DISTINCT FROM OLD.closed_at THEN
    RAISE EXCEPTION 'only the fulfilled amount, the cancellation reason, the due date and expiry of an open commitment can change'
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.status IS DISTINCT FROM OLD.status AND NEW.status IS DISTINCT FROM 'expired' THEN
    RAISE EXCEPTION 'the state of a commitment is derived by the kernel, not written'
      USING ERRCODE = 'restrict_violation';
  END IF;

  IF NEW.fulfilled_amount IS DISTINCT FROM OLD.fulfilled_amount THEN
    effects := effects + 1;
  END IF;
  IF NEW.canceled_reason IS DISTINCT FROM OLD.canceled_reason THEN
    effects := effects + 1;
  END IF;
  IF NEW.status IS DISTINCT FROM OLD.status THEN
    effects := effects + 1;
  END IF;
  IF NEW.due_at IS DISTINCT FROM OLD.due_at THEN
    effects := effects + 1;
  END IF;
  IF effects = 0 THEN
    RETURN NEW;
  END IF;
  IF effects > 1 THEN
    RAISE EXCEPTION 'a commitment changes in one way at a time' USING ERRCODE = 'restrict_violation';
  END IF;

  IF NEW.due_at IS DISTINCT FROM OLD.due_at THEN
    -- Strictly after now(): now() is the start of this transaction, already in
    -- the past for the rest of it, and a due date equal to it could be fired at once.
    IF NEW.due_at IS NULL OR NEW.due_at <= now() THEN
      RAISE EXCEPTION 'a commitment can only be rescheduled to a time in the future'
        USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'commitments_reschedule_past';
    END IF;
    RETURN NEW;
  END IF;

  IF NEW.fulfilled_amount IS DISTINCT FROM OLD.fulfilled_amount THEN
    -- Read through row level security: with no current space the sum is zero
    -- and the only value that passes is the one that is already there.
    SELECT COALESCE(sum(f.amount), 0) INTO fulfilled
    FROM kernel.fulfillments f
    WHERE f.commitment_id = OLD.id AND f.space_id = OLD.space_id;
    IF NEW.fulfilled_amount IS DISTINCT FROM fulfilled THEN
      RAISE EXCEPTION 'the fulfilled amount must equal the sum of the fulfillments'
        USING ERRCODE = 'restrict_violation';
    END IF;
    IF NEW.fulfilled_amount = NEW.amount THEN
      NEW.status := 'fulfilled';
    END IF;
  ELSIF NEW.canceled_reason IS DISTINCT FROM OLD.canceled_reason THEN
    -- An open commitment has no reason (a CHECK ties the two), so a different
    -- one is a reason being written for the first time.
    NEW.status := 'canceled';
  ELSE
    -- Expiry. The timer is read through row level security, so with no current
    -- space it is not seen and the expiry is refused.
    IF OLD.due_at > now() OR NOT EXISTS (
         SELECT 1 FROM kernel.timers t
         WHERE t.commitment_id = OLD.id AND t.space_id = OLD.space_id
           AND t.status = 'fired' AND t.fired_at = now() AND t.fire_at <= now()) THEN
      RAISE EXCEPTION 'a commitment expires only when its due date has passed and its timer has fired in this transaction'
        USING ERRCODE = 'restrict_violation';
    END IF;
  END IF;
  -- Unchanged by a fulfillment that leaves the commitment open.
  IF NEW.status <> 'open' THEN
    NEW.closed_at := now();
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- Second layer for the timer rules, checked when the transaction commits, for
-- every writer: an open commitment has exactly one scheduled timer and it fires
-- at the due time; a closed one has none; and a timer that fired belongs to an
-- expired commitment whose expiry is in the history of events, so the law of
-- memory holds even for a writer that skips the kernel operations. It reads
-- through row level security, so a transaction that changes its current space
-- before committing sees no commitment and fails, never passes.
-- +goose StatementBegin
CREATE FUNCTION kernel.check_timer_coherence() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  target uuid;
  commitment_status text;
  commitment_due timestamptz;
  commitment_space uuid;
  scheduled integer;
  on_due integer;
  fired integer;
BEGIN
  IF TG_TABLE_NAME = 'commitments' THEN
    target := NEW.id;
  ELSE
    target := NEW.commitment_id;
  END IF;
  SELECT c.status, c.due_at, c.space_id INTO commitment_status, commitment_due, commitment_space
  FROM kernel.commitments c WHERE c.id = target;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'the commitment of the timers is not visible'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'timers_coherent';
  END IF;
  SELECT count(*) FILTER (WHERE t.status = 'scheduled'),
         count(*) FILTER (WHERE t.status = 'scheduled' AND t.fire_at = commitment_due),
         count(*) FILTER (WHERE t.status = 'fired')
  INTO scheduled, on_due, fired
  FROM kernel.timers t WHERE t.commitment_id = target AND t.space_id = commitment_space;

  IF commitment_status = 'open' THEN
    IF scheduled <> 1 OR on_due <> 1 THEN
      RAISE EXCEPTION 'an open commitment has exactly one scheduled timer, at its due time'
        USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'timers_coherent';
    END IF;
  ELSIF scheduled <> 0 THEN
    RAISE EXCEPTION 'a closed commitment has no scheduled timer'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'timers_coherent';
  END IF;
  IF fired > 0 THEN
    IF commitment_status <> 'expired' OR NOT EXISTS (
         SELECT 1 FROM kernel.events e
         WHERE e.space_id = commitment_space AND e.type = 'kernel.commitment.expired' AND e.version = 1
           AND e.subject_ref = 'kernel.commitment/' || target::text) THEN
      RAISE EXCEPTION 'a fired timer needs an expired commitment and the event of its expiry'
        USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'timers_coherent';
    END IF;
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER timers_coherence_check AFTER INSERT OR UPDATE ON kernel.timers
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION kernel.check_timer_coherence();
CREATE CONSTRAINT TRIGGER timers_coherence_check AFTER INSERT OR UPDATE ON kernel.commitments
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION kernel.check_timer_coherence();

-- Timers are never deleted, whoever asks (ADR-0028). TRUNCATE ... CASCADE from
-- a parent table fires this trigger too.
-- +goose StatementBegin
CREATE FUNCTION kernel.reject_timer_removal() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  RAISE EXCEPTION 'timers cannot be deleted' USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER timers_no_delete BEFORE DELETE ON kernel.timers
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_timer_removal();
CREATE TRIGGER timers_no_truncate BEFORE TRUNCATE ON kernel.timers
  FOR EACH STATEMENT EXECUTE FUNCTION kernel.reject_timer_removal();

-- +goose Down
DROP TRIGGER timers_coherence_check ON kernel.commitments;
DROP TRIGGER commitments_timer_sync ON kernel.commitments;
DROP TRIGGER commitments_timer_create ON kernel.commitments;
REVOKE UPDATE (due_at, status) ON kernel.commitments FROM nexusthrive_api;
DROP TABLE kernel.timers;
DROP INDEX kernel.events_expired_subject_idx;
DROP FUNCTION kernel.reject_timer_removal();
DROP FUNCTION kernel.check_timer_coherence();
DROP FUNCTION kernel.commitments_timer_sync();
DROP FUNCTION kernel.commitments_timer_create();
DROP FUNCTION kernel.timers_expire_commitment();
DROP FUNCTION kernel.timers_guard_update();
DROP FUNCTION kernel.timers_guard_insert();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION kernel.guard_commitment_update() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  effects integer := 0;
  fulfilled numeric;
BEGIN
  IF OLD.status <> 'open' THEN
    RAISE EXCEPTION 'a closed commitment cannot change' USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.space_id IS DISTINCT FROM OLD.space_id
     OR NEW.kind IS DISTINCT FROM OLD.kind
     OR NEW.debtor_participant_id IS DISTINCT FROM OLD.debtor_participant_id
     OR NEW.creditor_participant_id IS DISTINCT FROM OLD.creditor_participant_id
     OR NEW.resource_id IS DISTINCT FROM OLD.resource_id OR NEW.amount IS DISTINCT FROM OLD.amount
     OR NEW.starts_at IS DISTINCT FROM OLD.starts_at OR NEW.due_at IS DISTINCT FROM OLD.due_at
     OR NEW.origin_ref IS DISTINCT FROM OLD.origin_ref OR NEW.created_at IS DISTINCT FROM OLD.created_at
     OR NEW.created_by_participant_id IS DISTINCT FROM OLD.created_by_participant_id
     OR NEW.closed_at IS DISTINCT FROM OLD.closed_at THEN
    RAISE EXCEPTION 'only the fulfilled amount, the cancellation reason and expiry of an open commitment can change'
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.status IS DISTINCT FROM OLD.status AND NEW.status IS DISTINCT FROM 'expired' THEN
    RAISE EXCEPTION 'the state of a commitment is derived by the kernel, not written'
      USING ERRCODE = 'restrict_violation';
  END IF;

  IF NEW.fulfilled_amount IS DISTINCT FROM OLD.fulfilled_amount THEN
    effects := effects + 1;
  END IF;
  IF NEW.canceled_reason IS DISTINCT FROM OLD.canceled_reason THEN
    effects := effects + 1;
  END IF;
  IF NEW.status IS DISTINCT FROM OLD.status THEN
    effects := effects + 1;
  END IF;
  IF effects = 0 THEN
    RETURN NEW;
  END IF;
  IF effects > 1 THEN
    RAISE EXCEPTION 'a commitment changes in one way at a time' USING ERRCODE = 'restrict_violation';
  END IF;

  IF NEW.fulfilled_amount IS DISTINCT FROM OLD.fulfilled_amount THEN
    SELECT COALESCE(sum(f.amount), 0) INTO fulfilled
    FROM kernel.fulfillments f
    WHERE f.commitment_id = OLD.id AND f.space_id = OLD.space_id;
    IF NEW.fulfilled_amount IS DISTINCT FROM fulfilled THEN
      RAISE EXCEPTION 'the fulfilled amount must equal the sum of the fulfillments'
        USING ERRCODE = 'restrict_violation';
    END IF;
    IF NEW.fulfilled_amount = NEW.amount THEN
      NEW.status := 'fulfilled';
    END IF;
  ELSIF NEW.canceled_reason IS DISTINCT FROM OLD.canceled_reason THEN
    NEW.status := 'canceled';
  END IF;
  IF NEW.status <> 'open' THEN
    NEW.closed_at := now();
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
