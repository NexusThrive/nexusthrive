-- +goose Up
-- The double-entry ledger (ADR-0030). An exchange moves resources between
-- accounts through entries that add up to zero per resource. Both tables are
-- append-only: history is corrected with another exchange, never edited.
CREATE TABLE kernel.exchanges (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  -- Two or more lowercase segments, as for event types.
  kind text NOT NULL CHECK (char_length(kind) <= 128 AND kind ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
  idempotency_key text NOT NULL CHECK (btrim(idempotency_key) <> '' AND char_length(idempotency_key) <= 255),
  actor_participant_id uuid NOT NULL,
  reverses_exchange_id uuid,
  memo text CHECK (memo IS NULL OR btrim(memo) <> ''),
  metadata jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(metadata) = 'object'),
  occurred_at timestamptz NOT NULL DEFAULT now(),
  -- Stamped by a trigger: no role chooses it.
  recorded_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT exchanges_idempotency_key UNIQUE (space_id, idempotency_key),
  -- An exchange is reversed once; nulls never collide, so ordinary exchanges are free.
  CONSTRAINT exchanges_reverses_once_key UNIQUE (space_id, reverses_exchange_id),
  -- Target of the composite keys of entries and of the reversal key.
  CONSTRAINT exchanges_id_space_key UNIQUE (id, space_id),
  CONSTRAINT exchanges_not_self_reverse CHECK (reverses_exchange_id IS NULL OR reverses_exchange_id <> id),
  CONSTRAINT exchanges_actor_fkey FOREIGN KEY (actor_participant_id, space_id)
    REFERENCES kernel.participants (id, space_id),
  CONSTRAINT exchanges_reverses_fkey FOREIGN KEY (reverses_exchange_id, space_id)
    REFERENCES kernel.exchanges (id, space_id)
);

CREATE TABLE kernel.entries (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  exchange_id uuid NOT NULL,
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  account_id uuid NOT NULL,
  resource_id uuid NOT NULL,
  -- Positive enters the account and negative leaves it. Zero, NaN and the
  -- infinities are refused: NaN and Infinity compare above every finite value.
  amount numeric NOT NULL CONSTRAINT entries_amount_check
    CHECK (amount <> 0 AND amount < 1e30 AND amount > -1e30),
  CONSTRAINT entries_exchange_fkey FOREIGN KEY (exchange_id, space_id)
    REFERENCES kernel.exchanges (id, space_id),
  -- The account must live in this space and hold this resource.
  CONSTRAINT entries_account_fkey FOREIGN KEY (account_id, space_id, resource_id)
    REFERENCES kernel.accounts (id, space_id, resource_id)
);

CREATE INDEX entries_exchange_idx ON kernel.entries (exchange_id);
CREATE INDEX entries_account_idx ON kernel.entries (space_id, account_id);

ALTER TABLE kernel.exchanges ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.exchanges FORCE ROW LEVEL SECURITY;
ALTER TABLE kernel.entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.entries FORCE ROW LEVEL SECURITY;

-- Per-command policies and none for UPDATE or DELETE: a change attempt by the
-- API role is refused for lack of privilege.
CREATE POLICY exchanges_select ON kernel.exchanges FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY exchanges_insert ON kernel.exchanges FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY entries_select ON kernel.entries FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY entries_insert ON kernel.entries FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));

-- Grants by column. recorded_at is left out, and a column added later is not
-- granted until a migration says so.
GRANT SELECT ON kernel.exchanges TO nexusthrive_api;
GRANT INSERT (id, space_id, kind, idempotency_key, actor_participant_id, reverses_exchange_id,
              memo, metadata, occurred_at)
  ON kernel.exchanges TO nexusthrive_api;
GRANT SELECT ON kernel.entries TO nexusthrive_api;
GRANT INSERT (id, exchange_id, space_id, account_id, resource_id, amount)
  ON kernel.entries TO nexusthrive_api;

-- Second layer of immutability (ADR-0028, ADR-0030). The triggers refuse any
-- change to the owner and the administrator too, and also when a privilege is
-- granted by mistake. TRUNCATE ... CASCADE from a parent table fires them.
-- +goose StatementBegin
CREATE FUNCTION kernel.reject_ledger_change() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  RAISE EXCEPTION 'the ledger is append-only' USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER exchanges_no_row_change BEFORE UPDATE OR DELETE ON kernel.exchanges
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_ledger_change();
CREATE TRIGGER exchanges_no_truncate BEFORE TRUNCATE ON kernel.exchanges
  FOR EACH STATEMENT EXECUTE FUNCTION kernel.reject_ledger_change();
CREATE TRIGGER entries_no_row_change BEFORE UPDATE OR DELETE ON kernel.entries
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_ledger_change();
CREATE TRIGGER entries_no_truncate BEFORE TRUNCATE ON kernel.entries
  FOR EACH STATEMENT EXECUTE FUNCTION kernel.reject_ledger_change();

-- The record time belongs to the kernel: whatever an insert writes, it is the
-- time of the transaction.
-- +goose StatementBegin
CREATE FUNCTION kernel.stamp_exchange() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  NEW.recorded_at := now();
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER exchanges_stamp BEFORE INSERT ON kernel.exchanges
  FOR EACH ROW EXECUTE FUNCTION kernel.stamp_exchange();

-- Guard of every entry, in this order:
--  a. the exchange was created in this same transaction, so no entry joins an
--     exchange that is already committed (history is not rewritten) and two
--     transactions cannot race on one exchange. Inside a savepoint the row
--     carries a subtransaction id and the guard rejects: it fails closed.
--  b. the account is open, holds the resource of the entry and lives in the
--     space. FOR SHARE conflicts with the UPDATE that closes the account and
--     not with other entries, so a close and an entry serialize; the foreign
--     key alone takes only KEY SHARE, which does not conflict with the close.
--     The status is read in PL, after any wait, to see the closed version.
--  c. the amount fits the scale of the resource, by value (50.100 fits two
--     decimals), and is stored in canonical form.
-- +goose StatementBegin
CREATE FUNCTION kernel.guard_entry_insert() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  account_status text;
  account_resource uuid;
  resource_scale integer;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM kernel.exchanges e
    WHERE e.id = NEW.exchange_id AND e.space_id = NEW.space_id
      AND e.xmin = pg_current_xact_id()::xid
  ) THEN
    RAISE EXCEPTION 'entries can only be added to an exchange created in the same transaction'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'entries_same_transaction';
  END IF;

  SELECT a.status, a.resource_id INTO account_status, account_resource
  FROM kernel.accounts a
  WHERE a.id = NEW.account_id AND a.space_id = NEW.space_id
  FOR SHARE;
  IF NOT FOUND OR account_status <> 'open' THEN
    RAISE EXCEPTION 'the account of an entry must exist in this space and be open'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'entries_account_open';
  END IF;
  IF account_resource <> NEW.resource_id THEN
    RAISE EXCEPTION 'the resource of an entry must be the resource of its account'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'entries_account_resource';
  END IF;

  SELECT r.scale INTO resource_scale
  FROM kernel.resources r
  WHERE r.id = NEW.resource_id AND r.space_id = NEW.space_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'the resource of an entry must exist in this space'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'entries_account_resource';
  END IF;
  IF NEW.amount <> round(NEW.amount, resource_scale) THEN
    RAISE EXCEPTION 'the amount has more decimals than the scale of its resource'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'entries_amount_scale';
  END IF;
  NEW.amount := round(NEW.amount, resource_scale);
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER entries_guard BEFORE INSERT ON kernel.entries
  FOR EACH ROW EXECUTE FUNCTION kernel.guard_entry_insert();

-- Conservation, checked when the transaction commits. It reads the entries
-- through row level security, so a transaction that changes its current space
-- before committing sees none and fails on the minimum, never passes. The
-- trigger on exchanges is the one that catches an exchange with no entries,
-- which the trigger on entries could never see; the second is defense in depth.
-- +goose StatementBegin
CREATE FUNCTION kernel.check_exchange_balance() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  target uuid;
  reversed uuid;
  total bigint;
BEGIN
  IF TG_TABLE_NAME = 'exchanges' THEN
    target := NEW.id;
  ELSE
    target := NEW.exchange_id;
  END IF;

  -- The count stops at 101 so that the cost of this check, which runs once per
  -- entry, stays bounded whatever an insert writes.
  SELECT count(*) INTO total FROM (
    SELECT 1 FROM kernel.entries WHERE exchange_id = target LIMIT 101
  ) AS bounded;
  IF total < 2 THEN
    RAISE EXCEPTION 'an exchange needs at least two entries'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'exchanges_min_entries';
  END IF;
  IF total > 100 THEN
    RAISE EXCEPTION 'an exchange has at most 100 entries'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'exchanges_max_entries';
  END IF;

  IF EXISTS (
    SELECT 1 FROM kernel.entries WHERE exchange_id = target
    GROUP BY resource_id HAVING sum(amount) <> 0
  ) THEN
    RAISE EXCEPTION 'the entries of an exchange must add up to zero per resource'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'exchanges_conservation';
  END IF;

  -- A reversal nets every account of the original to the opposite amount.
  SELECT x.reverses_exchange_id INTO reversed FROM kernel.exchanges x WHERE x.id = target;
  IF reversed IS NOT NULL AND EXISTS (
    SELECT 1
    FROM (SELECT e.account_id, sum(e.amount) AS net FROM kernel.entries e
          WHERE e.exchange_id = target GROUP BY e.account_id) AS fix
    FULL JOIN (SELECT e.account_id, sum(e.amount) AS net FROM kernel.entries e
               WHERE e.exchange_id = reversed GROUP BY e.account_id) AS orig
      USING (account_id)
    WHERE coalesce(fix.net, 0) <> -coalesce(orig.net, 0)
  ) THEN
    RAISE EXCEPTION 'a reversal must be the exact mirror of the exchange it reverses'
      USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'exchanges_reversal_mirror';
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER exchanges_balance_check AFTER INSERT ON kernel.exchanges
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION kernel.check_exchange_balance();
CREATE CONSTRAINT TRIGGER entries_balance_check AFTER INSERT ON kernel.entries
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION kernel.check_exchange_balance();

-- Follow-up 17: scale and kind of a resource are frozen once it has entries.
-- Row level security hides the entries of other spaces from the roles that
-- obey it, so the change must run in the space of the resource, and the table
-- lock waits for the inserts in flight before the check reads the entries.
-- +goose StatementBegin
CREATE FUNCTION kernel.guard_resource_change() RETURNS trigger
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

CREATE TRIGGER resources_guard_change BEFORE UPDATE ON kernel.resources
  FOR EACH ROW EXECUTE FUNCTION kernel.guard_resource_change();

-- Follow-up 19: the kernel stamps closed_at when an account closes, so the API
-- role cannot write a false date. The BEFORE triggers run before the CHECK
-- that ties closed_at to the status. The existing trigger is kept.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION kernel.reject_account_reopen() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  IF OLD.status = 'closed' AND (NEW.status <> 'closed' OR NEW.closed_at IS DISTINCT FROM OLD.closed_at) THEN
    RAISE EXCEPTION 'a closed account cannot be reopened' USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.status = 'open' AND NEW.status = 'closed' THEN
    NEW.closed_at := now();
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER resources_guard_change ON kernel.resources;
DROP FUNCTION kernel.guard_resource_change();
DROP TABLE kernel.entries;
DROP TABLE kernel.exchanges;
DROP FUNCTION kernel.check_exchange_balance();
DROP FUNCTION kernel.guard_entry_insert();
DROP FUNCTION kernel.stamp_exchange();
DROP FUNCTION kernel.reject_ledger_change();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION kernel.reject_account_reopen() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  IF OLD.status = 'closed' AND (NEW.status <> 'closed' OR NEW.closed_at IS DISTINCT FROM OLD.closed_at) THEN
    RAISE EXCEPTION 'a closed account cannot be reopened' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
