-- +goose Up
-- The API role never owns objects, never bypasses row level security and
-- cannot log in until the operator sets a password out of band (ADR-0027).
-- Roles belong to the cluster, so concurrent migrations of several databases
-- may race to create it; both losing outcomes are harmless.
-- +goose StatementBegin
DO $$
BEGIN
  CREATE ROLE nexusthrive_api NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION;
EXCEPTION WHEN duplicate_object OR unique_violation THEN
  NULL;
END
$$;
-- +goose StatementEnd

-- A pre-existing role with dangerous attributes would silently disable the
-- isolation, so refuse to continue.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'nexusthrive_api' AND (rolsuper OR rolbypassrls)) THEN
    RAISE EXCEPTION 'role nexusthrive_api must not be superuser nor bypass RLS';
  END IF;
END
$$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA kernel TO nexusthrive_api;
