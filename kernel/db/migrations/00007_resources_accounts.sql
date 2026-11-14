-- +goose Up
-- A resource is something a space can hold and move: money, units, time or
-- access. scale is the number of decimals an amount of it may carry. Resources
-- never change through the API: altering scale or kind after entries exist
-- would corrupt the ledger.
CREATE TABLE kernel.resources (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  kind text NOT NULL CONSTRAINT resources_kind_check CHECK (kind IN ('money', 'unit', 'time', 'access')),
  code text NOT NULL CONSTRAINT resources_code_check CHECK (code ~ '^[A-Z][A-Z0-9_]{0,31}$'),
  name text NOT NULL CHECK (btrim(name) <> ''),
  scale integer NOT NULL CONSTRAINT resources_scale_check CHECK (scale BETWEEN 0 AND 18),
  CONSTRAINT resources_space_code_key UNIQUE (space_id, code),
  -- Target of the composite keys of accounts: a resource must live in the space of its account.
  CONSTRAINT resources_id_space_key UNIQUE (id, space_id)
);

-- An account holds one resource for one participant. Every reference to space
-- data is a composite key with space_id, because foreign key checks ignore row
-- level security and a plain key could point into another space.
CREATE TABLE kernel.accounts (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  holder_participant_id uuid NOT NULL,
  resource_id uuid NOT NULL,
  parent_account_id uuid,
  class text NOT NULL CONSTRAINT accounts_class_check
    CHECK (class IN ('asset', 'liability', 'equity', 'income', 'expense')),
  code text NOT NULL CHECK (btrim(code) <> ''),
  name text NOT NULL CHECK (btrim(name) <> ''),
  status text NOT NULL DEFAULT 'open' CONSTRAINT accounts_status_check CHECK (status IN ('open', 'closed')),
  closed_at timestamptz,
  CONSTRAINT accounts_closed_coherent CHECK ((status = 'closed') = (closed_at IS NOT NULL)),
  CONSTRAINT accounts_not_own_parent CHECK (parent_account_id IS NULL OR parent_account_id <> id),
  CONSTRAINT accounts_holder_code_key UNIQUE (space_id, holder_participant_id, code),
  -- Target of the parent key and of the ledger entries: same space and same resource.
  CONSTRAINT accounts_id_space_resource_key UNIQUE (id, space_id, resource_id),
  CONSTRAINT accounts_holder_fkey FOREIGN KEY (holder_participant_id, space_id)
    REFERENCES kernel.participants (id, space_id),
  CONSTRAINT accounts_resource_fkey FOREIGN KEY (resource_id, space_id)
    REFERENCES kernel.resources (id, space_id),
  -- MATCH SIMPLE: with a null parent the key is not checked.
  CONSTRAINT accounts_parent_fkey FOREIGN KEY (parent_account_id, space_id, resource_id)
    REFERENCES kernel.accounts (id, space_id, resource_id)
);

ALTER TABLE kernel.resources ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.resources FORCE ROW LEVEL SECURITY;
ALTER TABLE kernel.accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.accounts FORCE ROW LEVEL SECURITY;

-- Per-command policies. Resources have no UPDATE or DELETE policy and accounts
-- have no DELETE policy: a change attempt is refused for lack of privilege.
CREATE POLICY resources_select ON kernel.resources FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY resources_insert ON kernel.resources FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY accounts_select ON kernel.accounts FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY accounts_insert ON kernel.accounts FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY accounts_update ON kernel.accounts FOR UPDATE
  USING (space_id = (SELECT kernel.current_space_id()))
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));

-- Explicit grants by column. Accounts are born open, so INSERT leaves out
-- status and closed_at, and closing is the only UPDATE. A column added later
-- is not granted until a migration says so.
GRANT SELECT ON kernel.resources TO nexusthrive_api;
GRANT INSERT (id, space_id, kind, code, name, scale) ON kernel.resources TO nexusthrive_api;
GRANT SELECT ON kernel.accounts TO nexusthrive_api;
GRANT INSERT (id, space_id, holder_participant_id, resource_id, parent_account_id, class, code, name)
  ON kernel.accounts TO nexusthrive_api;
GRANT UPDATE (status, closed_at) ON kernel.accounts TO nexusthrive_api;

-- A closed account stays closed. The UPDATE privilege on status would let the
-- API role reopen it, and the owner can always update, so the trigger refuses
-- the reopening for everyone, in the same way as the event guard.
-- +goose StatementBegin
CREATE FUNCTION kernel.reject_account_reopen() RETURNS trigger
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

CREATE TRIGGER accounts_no_reopen BEFORE UPDATE ON kernel.accounts
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_account_reopen();

-- +goose Down
DROP TRIGGER accounts_no_reopen ON kernel.accounts;
DROP FUNCTION kernel.reject_account_reopen();
DROP TABLE kernel.accounts;
DROP TABLE kernel.resources;
