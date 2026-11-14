-- +goose Up
-- An identity is someone who can sign in. It belongs to no space, so it has no
-- space_id and is visible only through a participant of the current space.
CREATE TABLE kernel.identities (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  provider text NOT NULL CHECK (provider IN ('clerk', 'api_key', 'system')),
  subject text NOT NULL CHECK (btrim(subject) <> ''),
  primary_email text CHECK (primary_email IS NULL OR btrim(primary_email) <> ''),
  display_name text NOT NULL CHECK (btrim(display_name) <> ''),
  last_seen_at timestamptz,
  disabled_at timestamptz,
  CONSTRAINT identities_provider_subject_key UNIQUE (provider, subject)
);

-- A participant is an identity, or someone without an account, inside one space.
CREATE TABLE kernel.participants (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  identity_id uuid REFERENCES kernel.identities (id),
  kind text NOT NULL CHECK (kind IN ('person', 'organization', 'agent', 'system')),
  display_name text NOT NULL CHECK (btrim(display_name) <> ''),
  legal_name text,
  document_type text CHECK (document_type IN ('CC', 'NIT', 'CE', 'PP')),
  document_number text,
  country_code text CHECK (country_code ~ '^[A-Z]{2}$'),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
  archived_at timestamptz,
  CHECK ((status = 'archived') = (archived_at IS NOT NULL)),
  CHECK ((document_type IS NULL) = (document_number IS NULL))
);

-- One participant per identity in each space. The index also serves the
-- visibility policy of identities.
CREATE UNIQUE INDEX participants_space_identity_key
  ON kernel.participants (space_id, identity_id) WHERE identity_id IS NOT NULL;

ALTER TABLE kernel.identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.identities FORCE ROW LEVEL SECURITY;
ALTER TABLE kernel.participants ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.participants FORCE ROW LEVEL SECURITY;

CREATE POLICY participants_isolation ON kernel.participants
  USING (space_id = (SELECT kernel.current_space_id()))
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));

-- Reads go through a participant of the current space. An archived participant
-- still makes its identity visible, so history stays readable.
CREATE POLICY identities_visible_via_participant ON kernel.identities FOR SELECT
  USING (EXISTS (SELECT 1 FROM kernel.participants p
                 WHERE p.identity_id = identities.id
                   AND p.space_id = (SELECT kernel.current_space_id())));

-- An identity is born before any participant, so insertion cannot depend on
-- the current space. The API role has no UPDATE, DELETE or RETURNING path, so
-- it cannot read back or change what it inserted.
CREATE POLICY identities_insert ON kernel.identities FOR INSERT WITH CHECK (true);

-- Explicit grants only. Participants may only be archived after creation, so
-- UPDATE is limited to the two columns that archiving changes.
GRANT SELECT, INSERT ON kernel.identities TO nexusthrive_api;
GRANT SELECT, INSERT ON kernel.participants TO nexusthrive_api;
GRANT UPDATE (status, archived_at) ON kernel.participants TO nexusthrive_api;
