-- +goose Up
CREATE TABLE kernel.webhook_endpoints (
 id uuid PRIMARY KEY CHECK (substr(id::text,15,1)='7'),
 space_id uuid NOT NULL REFERENCES kernel.spaces(id),
 url text NOT NULL CHECK (length(url) BETWEEN 1 AND 2048),
 events text[] NOT NULL CHECK (cardinality(events) BETWEEN 1 AND 100),
 secret_ciphertext bytea NOT NULL CHECK (octet_length(secret_ciphertext)=48),
 secret_nonce bytea NOT NULL CHECK (octet_length(secret_nonce)=12),
 encryption_key_id text NOT NULL CHECK (length(encryption_key_id) BETWEEN 1 AND 128),
 status text NOT NULL DEFAULT 'active' CHECK(status IN ('active','disabled')),
 consecutive_failures integer NOT NULL DEFAULT 0 CHECK(consecutive_failures>=0),
 disabled_reason text,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(id,space_id)
);
CREATE TABLE kernel.webhook_deliveries (
 id uuid PRIMARY KEY CHECK (substr(id::text,15,1)='7'),
 space_id uuid NOT NULL REFERENCES kernel.spaces(id),
 endpoint_id uuid NOT NULL,
 event_seq bigint NOT NULL,
 body bytea NOT NULL,
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','failed','sent','dead')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 claim_token uuid,
 claimed_until timestamptz,
 http_status integer CHECK(http_status BETWEEN 100 AND 599),
 duration_ms bigint CHECK(duration_ms>=0),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(id,space_id), UNIQUE(space_id,endpoint_id,event_seq),
 FOREIGN KEY(endpoint_id,space_id) REFERENCES kernel.webhook_endpoints(id,space_id),
 FOREIGN KEY(event_seq,space_id) REFERENCES kernel.events(seq,space_id),
 CHECK ((claim_token IS NULL)=(claimed_until IS NULL))
);
CREATE INDEX webhook_delivery_due ON kernel.webhook_deliveries(space_id,next_attempt_at,id) WHERE status IN ('pending','failed');
CREATE TABLE kernel.webhook_delivery_attempts (
 id uuid PRIMARY KEY CHECK(substr(id::text,15,1)='7'),
 space_id uuid NOT NULL REFERENCES kernel.spaces(id),
 delivery_id uuid NOT NULL,
 attempt integer NOT NULL CHECK(attempt BETWEEN 1 AND 5),
 outcome text NOT NULL CHECK(outcome IN ('sent','failed','claim_expired')),
 http_status integer CHECK(http_status BETWEEN 100 AND 599),
 duration_ms bigint NOT NULL CHECK(duration_ms>=0),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(delivery_id,attempt),
 FOREIGN KEY(delivery_id,space_id) REFERENCES kernel.webhook_deliveries(id,space_id)
);
ALTER TABLE kernel.webhook_endpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.webhook_endpoints FORCE ROW LEVEL SECURITY;
ALTER TABLE kernel.webhook_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.webhook_deliveries FORCE ROW LEVEL SECURITY;
ALTER TABLE kernel.webhook_delivery_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.webhook_delivery_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY webhook_endpoints_space ON kernel.webhook_endpoints USING(space_id=(SELECT kernel.current_space_id())) WITH CHECK(space_id=(SELECT kernel.current_space_id()));
CREATE POLICY webhook_deliveries_space ON kernel.webhook_deliveries USING(space_id=(SELECT kernel.current_space_id())) WITH CHECK(space_id=(SELECT kernel.current_space_id()));
CREATE POLICY webhook_attempts_select ON kernel.webhook_delivery_attempts FOR SELECT USING(space_id=(SELECT kernel.current_space_id()));
CREATE POLICY webhook_attempts_insert ON kernel.webhook_delivery_attempts FOR INSERT WITH CHECK(space_id=(SELECT kernel.current_space_id()));
GRANT SELECT,INSERT,UPDATE ON kernel.webhook_endpoints,kernel.webhook_deliveries TO nexusthrive_api;
GRANT SELECT,INSERT ON kernel.webhook_delivery_attempts TO nexusthrive_api;
CREATE TRIGGER webhook_attempts_no_change BEFORE UPDATE OR DELETE ON kernel.webhook_delivery_attempts FOR EACH ROW EXECUTE FUNCTION kernel.reject_event_change();
CREATE TRIGGER webhook_attempts_no_truncate BEFORE TRUNCATE ON kernel.webhook_delivery_attempts FOR EACH STATEMENT EXECUTE FUNCTION kernel.reject_event_change();

-- The worker directory has only the two columns needed to select active spaces.
-- +goose StatementBegin
DO $$ BEGIN
 CREATE ROLE nexusthrive_worker_directory NOLOGIN NOINHERIT NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION;
EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname='nexusthrive_worker_directory' AND (rolcanlogin OR rolinherit OR rolsuper OR rolbypassrls OR rolcreatedb OR rolcreaterole OR rolreplication))
 OR EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members m JOIN pg_catalog.pg_roles r ON r.oid=m.member WHERE r.rolname='nexusthrive_worker_directory')
 OR pg_catalog.pg_has_role('nexusthrive_api','nexusthrive_worker_directory','MEMBER') THEN
  RAISE EXCEPTION 'worker directory role assignment is unsafe';
 END IF;
END $$;
-- +goose StatementEnd
GRANT USAGE ON SCHEMA kernel TO nexusthrive_worker_directory;
GRANT SELECT(id,status) ON kernel.spaces TO nexusthrive_worker_directory;
CREATE POLICY worker_directory_spaces ON kernel.spaces FOR SELECT TO nexusthrive_worker_directory USING(status='active');
-- +goose StatementBegin
CREATE FUNCTION kernel.list_webhook_spaces(p_after uuid,p_limit integer)
RETURNS TABLE(space_id uuid) LANGUAGE sql STABLE SECURITY DEFINER
SET search_path=pg_catalog AS $$
 SELECT id FROM kernel.spaces WHERE status='active' AND (p_after IS NULL OR id>p_after)
 ORDER BY id LIMIT LEAST(GREATEST(p_limit,1),10)
$$;
-- +goose StatementEnd
ALTER FUNCTION kernel.list_webhook_spaces(uuid,integer) OWNER TO nexusthrive_worker_directory;
REVOKE ALL ON FUNCTION kernel.list_webhook_spaces(uuid,integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION kernel.list_webhook_spaces(uuid,integer) TO nexusthrive_api;

-- +goose Down
DROP FUNCTION kernel.list_webhook_spaces(uuid,integer);
DROP POLICY worker_directory_spaces ON kernel.spaces;
REVOKE SELECT(id,status) ON kernel.spaces FROM nexusthrive_worker_directory;
REVOKE USAGE ON SCHEMA kernel FROM nexusthrive_worker_directory;
DROP TABLE kernel.webhook_delivery_attempts;
DROP TABLE kernel.webhook_deliveries;
DROP TABLE kernel.webhook_endpoints;
