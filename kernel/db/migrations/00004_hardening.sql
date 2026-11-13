-- +goose Up
-- The role guard of 00001 only rejected superuser and BYPASSRLS. A role that
-- can create roles or databases, or replicate, also weakens isolation, so the
-- check now covers five attributes and runs on every deployment. If it fails,
-- fix the role with: ALTER ROLE nexusthrive_api NOCREATEROLE NOCREATEDB NOREPLICATION
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'nexusthrive_api' AND (rolsuper OR rolbypassrls
      OR rolcreaterole OR rolcreatedb OR rolreplication)) THEN
    RAISE EXCEPTION 'role nexusthrive_api has attributes that weaken isolation';
  END IF;
END $$;
-- +goose StatementEnd

-- A fixed search_path stops a function planted earlier in the caller's path
-- from replacing current_setting. CREATE OR REPLACE keeps the EXECUTE grant.
CREATE OR REPLACE FUNCTION kernel.current_space_id() RETURNS uuid
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp
AS $$ SELECT nullif(current_setting('kernel.space_id', true), '')::uuid $$;
