-- +goose Up
-- Returns the space fixed for the current transaction, or null when none is
-- set. After a COMMIT the parameter stays as an empty string in the session,
-- and casting it would raise an error, so empty counts as unset. A non-empty
-- malformed value still fails, which closes by default.
CREATE FUNCTION kernel.current_space_id() RETURNS uuid
LANGUAGE sql STABLE
AS $$ SELECT nullif(current_setting('kernel.space_id', true), '')::uuid $$;

GRANT EXECUTE ON FUNCTION kernel.current_space_id() TO nexusthrive_api;
