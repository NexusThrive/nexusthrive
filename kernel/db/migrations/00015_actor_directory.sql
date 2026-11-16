-- +goose Up
-- This role is cluster-global. Concurrent disposable databases may create it.
-- +goose StatementBegin
DO $$
BEGIN
  CREATE ROLE nexusthrive_directory NOLOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS
    NOCREATEDB NOCREATEROLE NOREPLICATION;
EXCEPTION WHEN duplicate_object OR unique_violation THEN
  NULL;
END
$$;
-- +goose StatementEnd

-- Never repair a privileged pre-existing role by silently changing it.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = 'nexusthrive_directory'
    AND (rolcanlogin OR rolinherit OR rolsuper OR rolbypassrls OR rolcreatedb OR rolcreaterole OR rolreplication)) THEN
    RAISE EXCEPTION 'directory role has unsafe attributes';
  END IF;
  IF EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members m
    JOIN pg_catalog.pg_roles r ON r.oid = m.member WHERE r.rolname = 'nexusthrive_directory')
    OR pg_catalog.pg_has_role('nexusthrive_api', 'nexusthrive_directory', 'MEMBER') THEN
    RAISE EXCEPTION 'directory role assignment is unsafe';
  END IF;
END
$$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA kernel TO nexusthrive_directory;
GRANT SELECT (id, slug) ON kernel.spaces TO nexusthrive_directory;
GRANT SELECT (id, provider, subject, disabled_at) ON kernel.identities TO nexusthrive_directory;
GRANT SELECT (id, space_id, identity_id, status) ON kernel.participants TO nexusthrive_directory;

-- These policies apply only while the fixed function runs as its dedicated owner.
CREATE POLICY directory_spaces ON kernel.spaces FOR SELECT TO nexusthrive_directory USING (true);
CREATE POLICY directory_identities ON kernel.identities FOR SELECT TO nexusthrive_directory USING (true);
CREATE POLICY directory_participants ON kernel.participants FOR SELECT TO nexusthrive_directory USING (true);

-- The application must validate the credential before supplying provider/subject.
-- The function exposes one matching participant link, never a space or identity list.
-- +goose StatementBegin
CREATE FUNCTION kernel.resolve_space_actor(p_provider text, p_subject text, p_slug text)
RETURNS TABLE (space_id uuid, identity_id uuid, participant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog
AS $$
  SELECT s.id, i.id, p.id
    FROM kernel.spaces s
    JOIN kernel.participants p ON p.space_id = s.id
    JOIN kernel.identities i ON i.id = p.identity_id
   WHERE s.slug = p_slug AND i.provider = p_provider AND i.subject = p_subject
     AND i.disabled_at IS NULL AND p.status = 'active'
$$;
-- +goose StatementEnd
ALTER FUNCTION kernel.resolve_space_actor(text, text, text) OWNER TO nexusthrive_directory;
REVOKE ALL ON FUNCTION kernel.resolve_space_actor(text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION kernel.resolve_space_actor(text, text, text) TO nexusthrive_api;
