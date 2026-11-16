-- +goose Up
ALTER TABLE kernel.webhook_endpoints
 ADD COLUMN previous_ciphertext bytea,
 ADD COLUMN previous_nonce bytea,
 ADD COLUMN previous_key_id text,
 ADD COLUMN previous_valid_until timestamptz,
 ADD CONSTRAINT webhook_previous_secret_complete CHECK (
  (previous_ciphertext IS NULL AND previous_nonce IS NULL AND previous_key_id IS NULL AND previous_valid_until IS NULL)
  OR (previous_ciphertext IS NOT NULL AND octet_length(previous_ciphertext)=48
   AND previous_nonce IS NOT NULL AND octet_length(previous_nonce)=12
   AND previous_key_id IS NOT NULL AND length(previous_key_id) BETWEEN 1 AND 128
   AND previous_valid_until IS NOT NULL));
ALTER TABLE kernel.webhook_deliveries ADD COLUMN replay_of uuid,
 ADD CONSTRAINT webhook_replay_space_fkey FOREIGN KEY(replay_of,space_id) REFERENCES kernel.webhook_deliveries(id,space_id);
ALTER TABLE kernel.webhook_deliveries DROP CONSTRAINT webhook_deliveries_space_id_endpoint_id_event_seq_key;
CREATE UNIQUE INDEX webhook_original_event ON kernel.webhook_deliveries(space_id,endpoint_id,event_seq) WHERE replay_of IS NULL;

-- +goose Down
-- Reverting after replay data exists fails safely rather than discarding history.
DROP INDEX kernel.webhook_original_event;
ALTER TABLE kernel.webhook_deliveries DROP COLUMN replay_of,
 ADD UNIQUE(space_id,endpoint_id,event_seq);
ALTER TABLE kernel.webhook_endpoints DROP CONSTRAINT webhook_previous_secret_complete,
 DROP COLUMN previous_ciphertext,DROP COLUMN previous_nonce,DROP COLUMN previous_key_id,DROP COLUMN previous_valid_until;
