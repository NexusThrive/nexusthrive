-- +goose Up
-- Composite key target: a grantor reference carries the kind of its space, and
-- this key ties the declared kind to the real one. kind cannot change while
-- anything references it (ON UPDATE NO ACTION, ADR-0029).
ALTER TABLE kernel.spaces ADD CONSTRAINT spaces_id_kind_key UNIQUE (id, kind);

-- A permission list has no null element, at most 200 elements, and each
-- element has two or more lowercase segments separated by dots. It is a
-- function because a CHECK cannot read each element of an array.
-- +goose StatementBegin
CREATE FUNCTION kernel.valid_permission_list(list text[]) RETURNS boolean
LANGUAGE sql IMMUTABLE
SET search_path = pg_catalog, pg_temp
AS $$
  SELECT cardinality(list) <= 200
    AND (cardinality(list) = 0 OR array_ndims(list) = 1)
    AND NOT EXISTS (
      SELECT 1 FROM unnest(list) AS element
      WHERE element IS NULL OR element !~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$')
$$;
-- +goose StatementEnd

-- A role is a named set of permissions defined by one space.
CREATE TABLE kernel.roles (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  key text NOT NULL CONSTRAINT roles_key_check CHECK (key ~ '^[a-z][a-z0-9_]{0,63}$'),
  name text NOT NULL CHECK (btrim(name) <> ''),
  permissions text[] NOT NULL CONSTRAINT roles_permissions_check CHECK (kernel.valid_permission_list(permissions)),
  system boolean NOT NULL,
  CONSTRAINT roles_space_key_key UNIQUE (space_id, key),
  -- Target of the composite key of grants: the role must live in the space of the grant.
  CONSTRAINT roles_id_space_key UNIQUE (id, space_id)
);

-- A grant gives a participant a role in a space. The grantor may live in
-- another space (support access), so its space and the kind of that space are
-- columns with their own composite keys: the grantor must exist in the space it
-- declares, and the declared kind must be the real one.
CREATE TABLE kernel.grants (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  participant_id uuid NOT NULL,
  role_id uuid NOT NULL,
  extra_permissions text[] NOT NULL DEFAULT '{}' CONSTRAINT grants_extra_permissions_check CHECK (kernel.valid_permission_list(extra_permissions)),
  granted_by uuid,
  granted_by_space_id uuid,
  granted_by_space_kind text,
  reason text CHECK (reason IS NULL OR btrim(reason) <> ''),
  expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  revoked_at timestamptz,
  CONSTRAINT grants_grantor_all_or_none CHECK (
    (granted_by IS NULL) = (granted_by_space_id IS NULL)
    AND (granted_by IS NULL) = (granted_by_space_kind IS NULL)),
  CONSTRAINT grants_no_self_grant CHECK (granted_by IS NULL OR granted_by <> participant_id),
  -- Only the space itself or a platform space grants in a space.
  CONSTRAINT grants_grantor_scope CHECK (
    granted_by IS NULL OR granted_by_space_id = space_id OR granted_by_space_kind = 'platform'),
  -- Support access: a grantor of a platform space acting in another space
  -- leaves a reason and a deadline (rule 10).
  CONSTRAINT grants_support_reason_expiry CHECK (
    granted_by IS NULL OR granted_by_space_id = space_id OR granted_by_space_kind <> 'platform'
    OR (reason IS NOT NULL AND expires_at IS NOT NULL)),
  CONSTRAINT grants_participant_fkey FOREIGN KEY (participant_id, space_id)
    REFERENCES kernel.participants (id, space_id),
  CONSTRAINT grants_role_fkey FOREIGN KEY (role_id, space_id)
    REFERENCES kernel.roles (id, space_id),
  -- MATCH SIMPLE: without a grantor the keys are not checked.
  CONSTRAINT grants_granted_by_fkey FOREIGN KEY (granted_by, granted_by_space_id)
    REFERENCES kernel.participants (id, space_id),
  CONSTRAINT grants_grantor_kind_fkey FOREIGN KEY (granted_by_space_id, granted_by_space_kind)
    REFERENCES kernel.spaces (id, kind),
  -- Target of the key of the audit entries.
  CONSTRAINT grants_id_space_key UNIQUE (id, space_id)
);

-- One active grant per participant and role. A revoked grant leaves room for a new one.
CREATE UNIQUE INDEX grants_active_key ON kernel.grants (space_id, participant_id, role_id)
  WHERE revoked_at IS NULL;

-- The capabilities active in a space. The key is the pair (space, capability).
-- status is not inserted: a capability is born active.
CREATE TABLE kernel.space_capabilities (
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  capability_key text NOT NULL CONSTRAINT space_capabilities_key_check CHECK (
    char_length(capability_key) <= 128 AND capability_key ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
  status text NOT NULL DEFAULT 'active' CONSTRAINT space_capabilities_status_check
    CHECK (status IN ('active', 'suspended')),
  source text NOT NULL CONSTRAINT space_capabilities_source_check
    CHECK (source IN ('package', 'ai', 'admin', 'code')),
  enabled_by uuid,
  enabled_by_space_id uuid,
  enabled_by_space_kind text,
  reason text CHECK (reason IS NULL OR btrim(reason) <> ''),
  enabled_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (space_id, capability_key),
  CONSTRAINT space_capabilities_enabler_all_or_none CHECK (
    (enabled_by IS NULL) = (enabled_by_space_id IS NULL)
    AND (enabled_by IS NULL) = (enabled_by_space_kind IS NULL)),
  -- Business owners never enable capabilities (rule 9): the enabler belongs to
  -- the space itself or to a platform space.
  CONSTRAINT space_capabilities_enabler_scope CHECK (
    enabled_by IS NULL OR enabled_by_space_id = space_id OR enabled_by_space_kind = 'platform'),
  CONSTRAINT space_capabilities_admin_enabler CHECK (source <> 'admin' OR enabled_by IS NOT NULL),
  CONSTRAINT space_capabilities_enabled_by_fkey FOREIGN KEY (enabled_by, enabled_by_space_id)
    REFERENCES kernel.participants (id, space_id),
  CONSTRAINT space_capabilities_enabler_kind_fkey FOREIGN KEY (enabled_by_space_id, enabled_by_space_kind)
    REFERENCES kernel.spaces (id, kind)
);

-- Every read or write by a privileged actor, with a reason and a request id.
-- space_id is the audited space; the actor lives in actor_space_id.
CREATE TABLE kernel.access_audit (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  actor_participant_id uuid NOT NULL,
  actor_space_id uuid NOT NULL,
  actor_space_kind text NOT NULL,
  action text NOT NULL CONSTRAINT access_audit_action_check CHECK (action IN ('read', 'write')),
  subject_ref text CHECK (subject_ref IS NULL OR btrim(subject_ref) <> ''),
  reason text NOT NULL CHECK (btrim(reason) <> ''),
  request_id text NOT NULL CHECK (btrim(request_id) <> ''),
  grant_id uuid,
  occurred_at timestamptz NOT NULL DEFAULT now(),
  -- The actor belongs to the audited space or to a platform space, and the
  -- declared kind is tied to the real one by the key to spaces.
  CONSTRAINT access_audit_actor_scope CHECK (
    actor_space_id = space_id OR actor_space_kind = 'platform'),
  CONSTRAINT access_audit_actor_fkey FOREIGN KEY (actor_participant_id, actor_space_id)
    REFERENCES kernel.participants (id, space_id),
  CONSTRAINT access_audit_actor_kind_fkey FOREIGN KEY (actor_space_id, actor_space_kind)
    REFERENCES kernel.spaces (id, kind),
  -- MATCH SIMPLE: without a grant the key is not checked.
  CONSTRAINT access_audit_grant_fkey FOREIGN KEY (grant_id, space_id)
    REFERENCES kernel.grants (id, space_id)
);

CREATE INDEX access_audit_space_time_idx ON kernel.access_audit (space_id, occurred_at);

ALTER TABLE kernel.roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.roles FORCE ROW LEVEL SECURITY;
ALTER TABLE kernel.grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.grants FORCE ROW LEVEL SECURITY;
ALTER TABLE kernel.space_capabilities ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.space_capabilities FORCE ROW LEVEL SECURITY;
ALTER TABLE kernel.access_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.access_audit FORCE ROW LEVEL SECURITY;

-- Per-command policies. Roles, capabilities and audit entries have no UPDATE
-- or DELETE policy; grants have no DELETE policy.
CREATE POLICY roles_select ON kernel.roles FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY roles_insert ON kernel.roles FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY grants_select ON kernel.grants FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY grants_insert ON kernel.grants FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY grants_update ON kernel.grants FOR UPDATE
  USING (space_id = (SELECT kernel.current_space_id()))
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY space_capabilities_select ON kernel.space_capabilities FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY space_capabilities_insert ON kernel.space_capabilities FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY access_audit_select ON kernel.access_audit FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY access_audit_insert ON kernel.access_audit FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));

-- Explicit grants by column. The columns the kernel sets itself are left out:
-- created_at, revoked_at, enabled_at, status and occurred_at. Revoking is the
-- only UPDATE. A column added later is not granted until a migration says so.
GRANT SELECT ON kernel.roles, kernel.grants, kernel.space_capabilities, kernel.access_audit
  TO nexusthrive_api;
GRANT INSERT (id, space_id, key, name, permissions, system) ON kernel.roles TO nexusthrive_api;
GRANT INSERT (id, space_id, participant_id, role_id, extra_permissions, granted_by,
              granted_by_space_id, granted_by_space_kind, reason, expires_at)
  ON kernel.grants TO nexusthrive_api;
GRANT UPDATE (revoked_at) ON kernel.grants TO nexusthrive_api;
GRANT INSERT (space_id, capability_key, source, enabled_by, enabled_by_space_id,
              enabled_by_space_kind, reason)
  ON kernel.space_capabilities TO nexusthrive_api;
GRANT INSERT (id, space_id, actor_participant_id, actor_space_id, actor_space_kind, action, subject_ref,
              reason, request_id, grant_id)
  ON kernel.access_audit TO nexusthrive_api;

-- A grant changes only by being revoked, and revoking is once. The privilege
-- limits the API role to revoked_at, but the owner can always update, and the
-- API role could write any date. The trigger refuses every other change, and
-- a change to a grant that is already revoked, for everyone, and stamps the
-- revocation with the database clock so that it cannot be forged.
-- +goose StatementBegin
CREATE FUNCTION kernel.guard_grant_update() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  IF OLD.revoked_at IS NOT NULL THEN
    RAISE EXCEPTION 'a revoked grant cannot change' USING ERRCODE = 'restrict_violation';
  END IF;
  IF to_jsonb(NEW) - 'revoked_at' <> to_jsonb(OLD) - 'revoked_at' THEN
    RAISE EXCEPTION 'a grant changes only by being revoked' USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.revoked_at IS NOT NULL THEN
    NEW.revoked_at := now();
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER grants_guard_update BEFORE UPDATE ON kernel.grants
  FOR EACH ROW EXECUTE FUNCTION kernel.guard_grant_update();

-- Grants are the memory of who had access: they are revoked, never deleted.
-- +goose StatementBegin
CREATE FUNCTION kernel.reject_grant_delete() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  RAISE EXCEPTION 'grants are revoked, never deleted' USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER grants_no_delete BEFORE DELETE ON kernel.grants
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_grant_delete();
CREATE TRIGGER grants_no_truncate BEFORE TRUNCATE ON kernel.grants
  FOR EACH STATEMENT EXECUTE FUNCTION kernel.reject_grant_delete();

-- The kind of a space decides who may grant and audit in it, so it never
-- changes. The API role keeps UPDATE only on the state columns of a space
-- (privilege layer), and the trigger refuses a change of kind for everyone,
-- even for the owner or a privilege granted by mistake (trigger layer).
REVOKE UPDATE ON kernel.spaces FROM nexusthrive_api;
GRANT UPDATE (name, status, suspended_at, suspended_reason, closed_at) ON kernel.spaces TO nexusthrive_api;

-- The same holds for insertion. The row policy of spaces accepts any row whose
-- id is the current space, and the API role chooses that setting, so it could
-- create a space of kind platform and use it as grantor or audit actor. Spaces
-- of kind platform are provisioned at bootstrap by the owner of the migrations
-- or the administrator, never by the execution role. INSERT is also granted by
-- column, leaving out status, created_at and the suspension and closing
-- columns, which the kernel sets itself.
REVOKE INSERT ON kernel.spaces FROM nexusthrive_api;
GRANT INSERT (id, kind, name, slug, parent_space_id, locale, timezone, currency)
  ON kernel.spaces TO nexusthrive_api;

-- The condition is current_user = 'nexusthrive_api': current_user is the role
-- in effect, so it is true for a direct connection of the role and after SET
-- ROLE, including a superuser that switches to it. pg_has_role(..., 'member')
-- is not used because it is true for every superuser, which would block the
-- bootstrap. The API role cannot switch to another role, so it cannot dodge it.
-- +goose StatementBegin
CREATE FUNCTION kernel.reject_api_platform_space() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  IF NEW.kind = 'platform' AND current_user = 'nexusthrive_api' THEN
    RAISE EXCEPTION 'platform spaces are provisioned at bootstrap, not by the API role'
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER spaces_no_api_platform BEFORE INSERT ON kernel.spaces
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_api_platform_space();

-- +goose StatementBegin
CREATE FUNCTION kernel.reject_space_kind_change() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  RAISE EXCEPTION 'the kind of a space cannot change' USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER spaces_kind_immutable BEFORE UPDATE OF kind ON kernel.spaces
  FOR EACH ROW WHEN (OLD.kind IS DISTINCT FROM NEW.kind)
  EXECUTE FUNCTION kernel.reject_space_kind_change();

-- Audit entries are append-only in two layers, as events are (ADR-0028): no
-- privilege for the API role, and these triggers for everyone else.
-- +goose StatementBegin
CREATE FUNCTION kernel.reject_access_audit_change() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  RAISE EXCEPTION 'access audit entries are append-only' USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER access_audit_no_row_change BEFORE UPDATE OR DELETE ON kernel.access_audit
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_access_audit_change();
CREATE TRIGGER access_audit_no_truncate BEFORE TRUNCATE ON kernel.access_audit
  FOR EACH STATEMENT EXECUTE FUNCTION kernel.reject_access_audit_change();

-- +goose Down
DROP TRIGGER spaces_no_api_platform ON kernel.spaces;
DROP FUNCTION kernel.reject_api_platform_space();
REVOKE INSERT ON kernel.spaces FROM nexusthrive_api;
GRANT INSERT ON kernel.spaces TO nexusthrive_api;
DROP TRIGGER spaces_kind_immutable ON kernel.spaces;
DROP FUNCTION kernel.reject_space_kind_change();
REVOKE UPDATE ON kernel.spaces FROM nexusthrive_api;
GRANT UPDATE ON kernel.spaces TO nexusthrive_api;
DROP TABLE kernel.access_audit;
DROP TABLE kernel.space_capabilities;
DROP TABLE kernel.grants;
DROP TABLE kernel.roles;
DROP FUNCTION kernel.reject_access_audit_change();
DROP FUNCTION kernel.reject_grant_delete();
DROP FUNCTION kernel.guard_grant_update();
DROP FUNCTION kernel.valid_permission_list(text[]);
ALTER TABLE kernel.spaces DROP CONSTRAINT spaces_id_kind_key;
