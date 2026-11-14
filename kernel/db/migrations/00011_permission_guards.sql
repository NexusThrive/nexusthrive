-- +goose Up
-- Two guards the permission model needs from the database (ADR-0032). They add
-- no table, column or privilege: they are functions and triggers that hold for
-- every writer, the owner of the tables included. Every function fixes its
-- search_path and none is SECURITY DEFINER.

-- Permissions only a platform space may hold. The list is fixed here and
-- mirrors the PlatformOnly flags of the Go catalog; a test ties the two.
-- +goose StatementBegin
CREATE FUNCTION kernel.platform_only_permissions() RETURNS text[]
LANGUAGE sql IMMUTABLE
SET search_path = pg_catalog, pg_temp
AS $$
  SELECT ARRAY['kernel.capability.write']::text[]
$$;
-- +goose StatementEnd

-- A role or a grant of a space that is not a platform space never lists a
-- platform-only permission. The check fails closed: a space whose row cannot
-- be read is not a platform space. It runs when the list is inserted or
-- changed, so the owner of the tables cannot widen a role later either.
-- +goose StatementBegin
CREATE FUNCTION kernel.guard_platform_only_permissions() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  listed text[];
  owner_kind text;
BEGIN
  IF TG_TABLE_NAME = 'roles' THEN
    listed := NEW.permissions;
  ELSE
    listed := NEW.extra_permissions;
  END IF;
  IF listed && kernel.platform_only_permissions() THEN
    SELECT kind INTO owner_kind FROM kernel.spaces WHERE id = NEW.space_id;
    IF owner_kind IS DISTINCT FROM 'platform' THEN
      RAISE EXCEPTION 'a permission reserved to platform spaces cannot be listed here'
        USING ERRCODE = 'restrict_violation';
    END IF;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER roles_guard_platform_only BEFORE INSERT OR UPDATE OF permissions ON kernel.roles
  FOR EACH ROW EXECUTE FUNCTION kernel.guard_platform_only_permissions();
CREATE TRIGGER grants_guard_platform_only BEFORE INSERT OR UPDATE OF extra_permissions ON kernel.grants
  FOR EACH ROW EXECUTE FUNCTION kernel.guard_platform_only_permissions();

-- A closed space is terminal: it is not reopened and its closing date is not
-- erased or changed, by anyone. The date of closing is the clock of the
-- database, as revoked_at is for grants, so a writer cannot forge it. A space
-- that is not closed carries no closing date. Suspension is not terminal and
-- stays reversible until the platform layer decides who may reactivate.
-- +goose StatementBegin
CREATE FUNCTION kernel.guard_space_closed() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  IF OLD.status = 'closed' THEN
    IF NEW.status IS DISTINCT FROM OLD.status OR NEW.closed_at IS DISTINCT FROM OLD.closed_at THEN
      RAISE EXCEPTION 'a closed space stays closed' USING ERRCODE = 'restrict_violation';
    END IF;
  ELSIF NEW.status = 'closed' THEN
    NEW.closed_at := now();
  ELSIF NEW.closed_at IS NOT NULL THEN
    RAISE EXCEPTION 'only a closed space has a closing date' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER spaces_guard_closed BEFORE UPDATE ON kernel.spaces
  FOR EACH ROW EXECUTE FUNCTION kernel.guard_space_closed();

-- +goose Down
DROP TRIGGER spaces_guard_closed ON kernel.spaces;
DROP FUNCTION kernel.guard_space_closed();
DROP TRIGGER grants_guard_platform_only ON kernel.grants;
DROP TRIGGER roles_guard_platform_only ON kernel.roles;
DROP FUNCTION kernel.guard_platform_only_permissions();
DROP FUNCTION kernel.platform_only_permissions();
