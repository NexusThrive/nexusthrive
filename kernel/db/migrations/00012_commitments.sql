-- +goose Up
-- Commitments and their fulfillments (ADR-0033). A commitment is an obligation
-- of a debtor to a creditor over a resource. The kernel, not the caller, derives
-- its state, the fulfilled amount and the closing time: the API role inserts it
-- open and fulfillments move it forward. A fulfillment ties one ledger entry,
-- once and in full, to one commitment.

-- Target of the composite key of fulfillments: the entry must live in the same
-- space and hold the same resource as the commitment it fulfills.
ALTER TABLE kernel.entries ADD CONSTRAINT entries_id_space_resource_key UNIQUE (id, space_id, resource_id);

CREATE TABLE kernel.commitments (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  -- Two or more lowercase segments, as for event types.
  kind text NOT NULL CHECK (char_length(kind) <= 128 AND kind ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
  debtor_participant_id uuid NOT NULL,
  creditor_participant_id uuid NOT NULL,
  resource_id uuid NOT NULL,
  -- NaN and the infinities compare above every finite value, so "< 1e30" refuses them.
  amount numeric NOT NULL CONSTRAINT commitments_amount_check CHECK (amount > 0 AND amount < 1e30),
  fulfilled_amount numeric NOT NULL DEFAULT 0
    CONSTRAINT commitments_fulfilled_within_amount CHECK (fulfilled_amount >= 0 AND fulfilled_amount <= amount),
  starts_at timestamptz NOT NULL DEFAULT now(),
  -- No default: a commitment without a due date is not a commitment.
  due_at timestamptz NOT NULL,
  -- No default either. The only value an insert may carry is 'open'; later the
  -- kernel derives it, and no role writes it.
  status text NOT NULL CONSTRAINT commitments_status_check CHECK (status IN ('open', 'fulfilled', 'canceled', 'expired')),
  -- Opaque to the kernel: a pointer to whatever created the commitment.
  origin_ref text CHECK (origin_ref IS NULL OR (btrim(origin_ref) <> '' AND char_length(origin_ref) <= 255)),
  canceled_reason text CHECK (canceled_reason IS NULL OR (btrim(canceled_reason) <> '' AND char_length(canceled_reason) <= 500)),
  closed_at timestamptz,
  -- Stamped by a trigger: no role chooses it.
  created_at timestamptz NOT NULL DEFAULT now(),
  created_by_participant_id uuid NOT NULL,
  CONSTRAINT commitments_due_after_start CHECK (due_at >= starts_at),
  CONSTRAINT commitments_distinct_parties CHECK (debtor_participant_id <> creditor_participant_id),
  CONSTRAINT commitments_closed_coherent CHECK ((status = 'open') = (closed_at IS NULL)),
  CONSTRAINT commitments_canceled_coherent CHECK ((status = 'canceled') = (canceled_reason IS NOT NULL)),
  CONSTRAINT commitments_state_amount_coherent CHECK (
    (status <> 'open' OR fulfilled_amount < amount) AND (status <> 'fulfilled' OR fulfilled_amount = amount)),
  -- Targets of the composite keys of fulfillments and, later, of timers.
  CONSTRAINT commitments_id_space_key UNIQUE (id, space_id),
  CONSTRAINT commitments_id_space_resource_key UNIQUE (id, space_id, resource_id),
  CONSTRAINT commitments_debtor_fkey FOREIGN KEY (debtor_participant_id, space_id)
    REFERENCES kernel.participants (id, space_id),
  CONSTRAINT commitments_creditor_fkey FOREIGN KEY (creditor_participant_id, space_id)
    REFERENCES kernel.participants (id, space_id),
  CONSTRAINT commitments_created_by_fkey FOREIGN KEY (created_by_participant_id, space_id)
    REFERENCES kernel.participants (id, space_id),
  CONSTRAINT commitments_resource_fkey FOREIGN KEY (resource_id, space_id)
    REFERENCES kernel.resources (id, space_id)
);

CREATE TABLE kernel.fulfillments (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  commitment_id uuid NOT NULL,
  entry_id uuid NOT NULL,
  resource_id uuid NOT NULL,
  amount numeric NOT NULL CONSTRAINT fulfillments_amount_check CHECK (amount > 0 AND amount < 1e30),
  -- Stamped by a trigger: no role chooses it.
  created_at timestamptz NOT NULL DEFAULT now(),
  -- An entry is applied once, even by two transactions at the same time: the
  -- unique index serializes them.
  CONSTRAINT fulfillments_entry_key UNIQUE (entry_id),
  -- Space and resource travel in both keys, so a fulfillment cannot mix them.
  CONSTRAINT fulfillments_commitment_fkey FOREIGN KEY (commitment_id, space_id, resource_id)
    REFERENCES kernel.commitments (id, space_id, resource_id),
  CONSTRAINT fulfillments_entry_fkey FOREIGN KEY (entry_id, space_id, resource_id)
    REFERENCES kernel.entries (id, space_id, resource_id)
);

CREATE INDEX fulfillments_commitment_idx ON kernel.fulfillments (commitment_id);

ALTER TABLE kernel.commitments ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.commitments FORCE ROW LEVEL SECURITY;
ALTER TABLE kernel.fulfillments ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.fulfillments FORCE ROW LEVEL SECURITY;

CREATE POLICY commitments_select ON kernel.commitments FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY commitments_insert ON kernel.commitments FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY commitments_update ON kernel.commitments FOR UPDATE
  USING (space_id = (SELECT kernel.current_space_id()))
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY fulfillments_select ON kernel.fulfillments FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY fulfillments_insert ON kernel.fulfillments FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));

-- Grants by column. status is insertable only because it has no default, and
-- the trigger below admits only 'open'; it is never updatable. fulfilled_amount
-- is updatable because the triggers run with the rights of the writer, and the
-- trigger accepts no value but the sum of the fulfillments.
GRANT SELECT ON kernel.commitments TO nexusthrive_api;
GRANT INSERT (id, space_id, kind, debtor_participant_id, creditor_participant_id, resource_id,
              amount, starts_at, due_at, status, origin_ref, created_by_participant_id)
  ON kernel.commitments TO nexusthrive_api;
GRANT UPDATE (fulfilled_amount, canceled_reason) ON kernel.commitments TO nexusthrive_api;
GRANT SELECT ON kernel.fulfillments TO nexusthrive_api;
GRANT INSERT (id, space_id, commitment_id, entry_id, resource_id, amount)
  ON kernel.fulfillments TO nexusthrive_api;

-- Guard of every new commitment. It admits only an open commitment with
-- nothing fulfilled, no closing time and no cancellation reason, stamps the
-- creation time and keeps the amount inside the scale of its resource.
-- +goose StatementBegin
CREATE FUNCTION kernel.guard_commitment_insert() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  resource_scale integer;
BEGIN
  IF NEW.status IS NOT NULL AND NEW.status <> 'open' THEN
    RAISE EXCEPTION 'a commitment is created open' USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.fulfilled_amount IS DISTINCT FROM 0 OR NEW.closed_at IS NOT NULL OR NEW.canceled_reason IS NOT NULL THEN
    RAISE EXCEPTION 'a new commitment has nothing fulfilled, no closing time and no cancellation reason'
      USING ERRCODE = 'restrict_violation';
  END IF;
  NEW.created_at := now();

  SELECT r.scale INTO resource_scale
  FROM kernel.resources r
  WHERE r.id = NEW.resource_id AND r.space_id = NEW.space_id;
  -- A missing resource is reported by the composite foreign key.
  IF FOUND AND NEW.amount IS NOT NULL THEN
    IF NEW.amount <> round(NEW.amount, resource_scale) THEN
      RAISE EXCEPTION 'the amount has more decimals than the scale of its resource'
        USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'commitments_amount_scale';
    END IF;
    NEW.amount := round(NEW.amount, resource_scale);
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER commitments_guard_insert BEFORE INSERT ON kernel.commitments
  FOR EACH ROW EXECUTE FUNCTION kernel.guard_commitment_insert();

-- Guard of every change to a commitment. A closed commitment never changes.
-- An open one admits exactly one effect per statement, and the kernel derives
-- the state that follows from it:
--  fulfilled_amount  must equal the sum of the fulfillments; reaching the
--                    amount makes the commitment fulfilled.
--  canceled_reason   from null to a reason cancels the commitment.
--  status 'expired'  closes it as expired (the hook of the timer story).
-- Every effect stamps closed_at with the clock of the database. Whatever else
-- a caller writes is refused, the owner of the table included.
-- +goose StatementBegin
CREATE FUNCTION kernel.guard_commitment_update() RETURNS trigger
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
    IF OLD.canceled_reason IS NOT NULL OR NEW.canceled_reason IS NULL THEN
      RAISE EXCEPTION 'a cancellation reason is written once' USING ERRCODE = 'restrict_violation';
    END IF;
    NEW.status := 'canceled';
  END IF;
  -- Unchanged by a fulfillment that leaves the commitment open.
  IF NEW.status <> 'open' THEN
    NEW.closed_at := now();
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER commitments_guard_update BEFORE UPDATE ON kernel.commitments
  FOR EACH ROW EXECUTE FUNCTION kernel.guard_commitment_update();

-- Guard of every fulfillment, in this order:
--  a. the commitment is locked FOR NO KEY UPDATE. A second fulfillment, a
--     cancellation or an expiry of the same commitment waits here and rereads
--     the committed version, so the pending amount below is never stale.
--  b. the commitment is open.
--  c. the entry lives in this space and resource, enters an account of the
--     creditor with a positive amount, does not belong to a reversal, and is
--     applied in full.
--  d. the amount fits inside what is still pending.
-- Each statement of the function sees a fresh snapshot, so the lock wait
-- ends with the other transaction's work visible.
-- +goose StatementBegin
CREATE FUNCTION kernel.guard_fulfillment_insert() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  commitment_status text;
  commitment_amount numeric;
  commitment_fulfilled numeric;
  commitment_creditor uuid;
  entry_amount numeric;
  entry_holder uuid;
  entry_reverses uuid;
BEGIN
  SELECT c.status, c.amount, c.fulfilled_amount, c.creditor_participant_id
  INTO commitment_status, commitment_amount, commitment_fulfilled, commitment_creditor
  FROM kernel.commitments c
  WHERE c.id = NEW.commitment_id AND c.space_id = NEW.space_id AND c.resource_id = NEW.resource_id
  FOR NO KEY UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'the commitment must exist in this space and hold the resource of the fulfillment'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'fulfillments_commitment_fkey';
  END IF;
  IF commitment_status <> 'open' THEN
    RAISE EXCEPTION 'only an open commitment can be fulfilled'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'fulfillments_commitment_open';
  END IF;

  SELECT e.amount, a.holder_participant_id, x.reverses_exchange_id
  INTO entry_amount, entry_holder, entry_reverses
  FROM kernel.entries e
  JOIN kernel.accounts a ON a.id = e.account_id AND a.space_id = e.space_id
  JOIN kernel.exchanges x ON x.id = e.exchange_id AND x.space_id = e.space_id
  WHERE e.id = NEW.entry_id AND e.space_id = NEW.space_id AND e.resource_id = NEW.resource_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'the entry must exist in this space and hold the resource of the fulfillment'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'fulfillments_entry_fkey';
  END IF;
  IF entry_amount <= 0 OR entry_holder IS DISTINCT FROM commitment_creditor THEN
    RAISE EXCEPTION 'a fulfillment needs a positive entry in an account of the creditor'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'fulfillments_entry_creditor';
  END IF;
  IF entry_reverses IS NOT NULL THEN
    RAISE EXCEPTION 'an entry of a reversal cannot fulfill a commitment'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'fulfillments_entry_reversal';
  END IF;
  IF NEW.amount IS DISTINCT FROM entry_amount THEN
    RAISE EXCEPTION 'a fulfillment applies its entry in full'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'fulfillments_amount_entry';
  END IF;
  IF NEW.amount > commitment_amount - commitment_fulfilled THEN
    RAISE EXCEPTION 'the fulfillment exceeds the pending amount of the commitment'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'fulfillments_within_pending';
  END IF;
  NEW.created_at := now();
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER fulfillments_guard_insert BEFORE INSERT ON kernel.fulfillments
  FOR EACH ROW EXECUTE FUNCTION kernel.guard_fulfillment_insert();

-- The fulfilled amount of the commitment follows its fulfillments. The guard
-- of commitments validates the value and derives the state and the closing time.
-- +goose StatementBegin
CREATE FUNCTION kernel.apply_fulfillment() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  UPDATE kernel.commitments c
  SET fulfilled_amount = (
    SELECT COALESCE(sum(f.amount), 0) FROM kernel.fulfillments f
    WHERE f.commitment_id = NEW.commitment_id AND f.space_id = NEW.space_id)
  WHERE c.id = NEW.commitment_id AND c.space_id = NEW.space_id;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER fulfillments_apply AFTER INSERT ON kernel.fulfillments
  FOR EACH ROW EXECUTE FUNCTION kernel.apply_fulfillment();

-- Second layer for the fulfilled amount, checked when the transaction commits.
-- It reads through row level security, so a transaction that changes its
-- current space before committing sees no commitment and fails, never passes.
-- +goose StatementBegin
CREATE FUNCTION kernel.check_commitment_fulfilled() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  target uuid;
  stored numeric;
  total numeric;
BEGIN
  IF TG_TABLE_NAME = 'commitments' THEN
    target := NEW.id;
  ELSE
    target := NEW.commitment_id;
  END IF;
  SELECT c.fulfilled_amount INTO stored FROM kernel.commitments c WHERE c.id = target;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'the commitment is not visible to verify its fulfilled amount'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'commitments_fulfilled_sum';
  END IF;
  SELECT COALESCE(sum(f.amount), 0) INTO total FROM kernel.fulfillments f WHERE f.commitment_id = target;
  IF stored IS DISTINCT FROM total THEN
    RAISE EXCEPTION 'the fulfilled amount must equal the sum of the fulfillments'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'commitments_fulfilled_sum';
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER commitments_fulfilled_sum_check AFTER INSERT OR UPDATE ON kernel.commitments
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION kernel.check_commitment_fulfilled();
CREATE CONSTRAINT TRIGGER fulfillments_fulfilled_sum_check AFTER INSERT ON kernel.fulfillments
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION kernel.check_commitment_fulfilled();

-- Commitments are never deleted, whoever asks, and neither is the history of
-- fulfillments (ADR-0028, ADR-0030). TRUNCATE ... CASCADE from a parent table
-- fires these triggers too.
-- +goose StatementBegin
CREATE FUNCTION kernel.reject_commitment_removal() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  RAISE EXCEPTION 'commitments cannot be deleted' USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER commitments_no_delete BEFORE DELETE ON kernel.commitments
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_commitment_removal();
CREATE TRIGGER commitments_no_truncate BEFORE TRUNCATE ON kernel.commitments
  FOR EACH STATEMENT EXECUTE FUNCTION kernel.reject_commitment_removal();
CREATE TRIGGER fulfillments_no_row_change BEFORE UPDATE OR DELETE ON kernel.fulfillments
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_ledger_change();
CREATE TRIGGER fulfillments_no_truncate BEFORE TRUNCATE ON kernel.fulfillments
  FOR EACH STATEMENT EXECUTE FUNCTION kernel.reject_ledger_change();

-- Scale and kind of a resource stay frozen once it has entries (follow-up 17)
-- and now also once it has commitments: lowering the scale would leave amounts
-- the resource can no longer represent.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION kernel.guard_resource_change() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  IF NEW.scale IS DISTINCT FROM OLD.scale OR NEW.kind IS DISTINCT FROM OLD.kind THEN
    IF kernel.current_space_id() IS DISTINCT FROM OLD.space_id THEN
      RAISE EXCEPTION 'scale and kind of a resource change only from the space of the resource'
        USING ERRCODE = 'restrict_violation';
    END IF;
    LOCK TABLE kernel.entries IN SHARE MODE;
    IF EXISTS (SELECT 1 FROM kernel.entries e WHERE e.resource_id = OLD.id AND e.space_id = OLD.space_id) THEN
      RAISE EXCEPTION 'scale and kind of a resource with entries cannot change'
        USING ERRCODE = 'restrict_violation';
    END IF;
    LOCK TABLE kernel.commitments IN SHARE MODE;
    IF EXISTS (SELECT 1 FROM kernel.commitments c WHERE c.resource_id = OLD.id AND c.space_id = OLD.space_id) THEN
      RAISE EXCEPTION 'scale and kind of a resource with commitments cannot change'
        USING ERRCODE = 'restrict_violation';
    END IF;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION kernel.guard_resource_change() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  IF NEW.scale IS DISTINCT FROM OLD.scale OR NEW.kind IS DISTINCT FROM OLD.kind THEN
    IF kernel.current_space_id() IS DISTINCT FROM OLD.space_id THEN
      RAISE EXCEPTION 'scale and kind of a resource change only from the space of the resource'
        USING ERRCODE = 'restrict_violation';
    END IF;
    LOCK TABLE kernel.entries IN SHARE MODE;
    IF EXISTS (SELECT 1 FROM kernel.entries e WHERE e.resource_id = OLD.id AND e.space_id = OLD.space_id) THEN
      RAISE EXCEPTION 'scale and kind of a resource with entries cannot change'
        USING ERRCODE = 'restrict_violation';
    END IF;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
DROP TABLE kernel.fulfillments;
DROP TABLE kernel.commitments;
DROP FUNCTION kernel.reject_commitment_removal();
DROP FUNCTION kernel.check_commitment_fulfilled();
DROP FUNCTION kernel.apply_fulfillment();
DROP FUNCTION kernel.guard_fulfillment_insert();
DROP FUNCTION kernel.guard_commitment_update();
DROP FUNCTION kernel.guard_commitment_insert();
ALTER TABLE kernel.entries DROP CONSTRAINT entries_id_space_resource_key;
