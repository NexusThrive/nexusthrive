-- +goose Up
-- Composite key target, so that the actor of an event must live in the space of
-- the event. It adds no data rule: id is already unique on its own.
ALTER TABLE kernel.participants ADD CONSTRAINT participants_id_space_key UNIQUE (id, space_id);

-- Events are the memory of the system: append-only facts in one global order.
-- seq is generated always, so an ordinary insert cannot choose it, and the API
-- role has no INSERT privilege on that column, so it cannot override it either.
CREATE TABLE kernel.events (
  seq bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  id uuid NOT NULL UNIQUE CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  -- Two or more lowercase segments. The version is a column, so the type must
  -- not end in a version segment such as v1.
  type text NOT NULL CHECK (char_length(type) <= 128
    AND type ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'
    AND type !~ '\.v[0-9]+$'),
  version integer NOT NULL CHECK (version >= 1),
  subject_ref text CHECK (subject_ref IS NULL OR btrim(subject_ref) <> ''),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  actor_participant_id uuid,
  actor_kind text NOT NULL CHECK (actor_kind IN ('person', 'agent', 'api_key', 'system')),
  request_id text CHECK (request_id IS NULL OR btrim(request_id) <> ''),
  occurred_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT events_actor_required CHECK (actor_participant_id IS NOT NULL OR actor_kind = 'system'),
  CONSTRAINT events_actor_fkey FOREIGN KEY (actor_participant_id, space_id)
    REFERENCES kernel.participants (id, space_id)
);

CREATE INDEX events_space_seq_idx ON kernel.events (space_id, seq);

ALTER TABLE kernel.events ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.events FORCE ROW LEVEL SECURITY;

-- Per-command policies and no UPDATE or DELETE policy: a change attempt by the
-- API role is refused for lack of privilege, not silently matched to no rows.
CREATE POLICY events_select ON kernel.events FOR SELECT
  USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY events_insert ON kernel.events FOR INSERT
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));

-- INSERT is granted by column and leaves out seq. A table-wide INSERT would let
-- the API role write seq with OVERRIDING SYSTEM VALUE, and a value ahead of the
-- sequence would make every later insert of every space collide on the key.
GRANT SELECT ON kernel.events TO nexusthrive_api;
GRANT INSERT (id, space_id, type, version, subject_ref, payload, actor_participant_id,
              actor_kind, request_id, occurred_at)
  ON kernel.events TO nexusthrive_api;

-- Second layer of immutability (ADR-0028). Privileges protect only the API
-- role; the table owner and the administrator may update and delete by default,
-- and a later migration could grant the privilege by mistake. The triggers
-- refuse the change for everyone.
-- +goose StatementBegin
CREATE FUNCTION kernel.reject_event_change() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  RAISE EXCEPTION 'events are append-only' USING ERRCODE = 'restrict_violation';
END
$$;
-- +goose StatementEnd

CREATE TRIGGER events_no_row_change BEFORE UPDATE OR DELETE ON kernel.events
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_event_change();
CREATE TRIGGER events_no_truncate BEFORE TRUNCATE ON kernel.events
  FOR EACH STATEMENT EXECUTE FUNCTION kernel.reject_event_change();

-- +goose Down
DROP TABLE kernel.events;
DROP FUNCTION kernel.reject_event_change();
ALTER TABLE kernel.participants DROP CONSTRAINT participants_id_space_key;
