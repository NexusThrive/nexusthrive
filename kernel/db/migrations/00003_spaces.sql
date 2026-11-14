-- +goose Up
CREATE TABLE kernel.spaces (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  kind text NOT NULL CHECK (kind IN ('platform', 'business', 'community', 'personal')),
  name text NOT NULL CHECK (btrim(name) <> ''),
  slug text NOT NULL UNIQUE,
  status text NOT NULL DEFAULT 'provisioning'
    CHECK (status IN ('provisioning', 'active', 'suspended', 'closed')),
  parent_space_id uuid REFERENCES kernel.spaces (id) CHECK (parent_space_id <> id),
  locale text NOT NULL DEFAULT 'es-CO',
  timezone text NOT NULL DEFAULT 'America/Bogota',
  currency text NOT NULL DEFAULT 'COP',
  created_at timestamptz NOT NULL DEFAULT now(),
  suspended_at timestamptz,
  suspended_reason text,
  closed_at timestamptz
);

-- FORCE makes the policy apply to the table owner too; superusers and roles
-- with BYPASSRLS still skip it, which is why the API role is neither.
ALTER TABLE kernel.spaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.spaces FORCE ROW LEVEL SECURITY;

-- For spaces the row is the space, so the policy compares id. Tables of the
-- following stories compare space_id with (SELECT kernel.current_space_id()).
CREATE POLICY spaces_isolation ON kernel.spaces
  USING (id = kernel.current_space_id())
  WITH CHECK (id = kernel.current_space_id());

-- Explicit grants only: no DELETE or TRUNCATE, no default privileges.
GRANT SELECT, INSERT, UPDATE ON kernel.spaces TO nexusthrive_api;
