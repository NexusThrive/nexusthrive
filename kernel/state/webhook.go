package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// CreateWebhookEndpoint persists only the sealed signing key in the current space.
func CreateWebhookEndpoint(ctx context.Context, tx *sql.Tx, in model.NewWebhookEndpoint) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO kernel.webhook_endpoints(id,space_id,url,events,secret_ciphertext,secret_nonce,encryption_key_id) VALUES($1,kernel.current_space_id(),$2,$3,$4,$5,$6)`, in.ID, in.URL, in.Events, in.Secret.Ciphertext, in.Secret.Nonce, in.Secret.KeyID)
	if err != nil {
		return fmt.Errorf("create webhook endpoint: %w", err)
	}
	return nil
}

// FanoutWebhook materializes an immutable body and all matching jobs with the outbox handoff.
func FanoutWebhook(ctx context.Context, tx *sql.Tx, clock model.Clock, row model.OutboxRow) error {
	var seq int64
	err := tx.QueryRowContext(ctx, `SELECT event_seq FROM kernel.outbox WHERE event_seq=$1 AND subscriber='webhooks' AND claim_token=$2 AND claimed_until>$3 AND status IN ('pending','failed') FOR UPDATE`, row.EventSeq, row.ClaimToken, outboxTime(clock)).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return model.NewError(model.CodeRuleViolation, "webhook outbox claim lost", nil)
	}
	if err != nil {
		return fmt.Errorf("lock webhook outbox: %w", err)
	}
	events, err := ReadEventsTx(ctx, tx, seq, 1)
	if err != nil {
		return err
	}
	if len(events) != 1 || events[0].Seq != seq {
		return model.NewError(model.CodeNotFound, "webhook event not found", nil)
	}
	event := events[0]
	body, err := json.Marshal(map[string]any{"id": event.ID, "type": event.Type, "version": event.Version, "seq": strconv.FormatInt(event.Seq, 10), "space_id": event.SpaceID, "occurred_at": event.OccurredAt, "actor_kind": event.Actor.Kind, "actor_participant_id": event.Actor.ParticipantID, "request_id": event.RequestID, "payload": event.Payload})
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM kernel.webhook_endpoints WHERE status='active' AND events @> ARRAY[$1]::text[] ORDER BY id`, event.Type+".v"+strconv.Itoa(event.Version))
	if err != nil {
		return fmt.Errorf("select webhook endpoints: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, endpoint := range ids {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO kernel.webhook_deliveries(id,space_id,endpoint_id,event_seq,body,next_attempt_at) VALUES($1,kernel.current_space_id(),$2,$3,$4,$5) ON CONFLICT(space_id,endpoint_id,event_seq) DO NOTHING`, id.String(), endpoint, event.Seq, body, outboxTime(clock))
		if err != nil {
			return fmt.Errorf("materialize webhook: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE kernel.outbox SET status='sent',delivered_at=$3,claim_token=NULL,claimed_until=NULL WHERE event_seq=$1 AND subscriber='webhooks' AND claim_token=$2`, seq, row.ClaimToken, outboxTime(clock))
	return err
}

// ClaimWebhookDelivery returns one committed lease; HTTP must occur after commit.
func ClaimWebhookDelivery(ctx context.Context, tx *sql.Tx, clock model.Clock) (*model.WebhookJob, error) {
	now := outboxTime(clock)
	// Recover expired leases in a bounded batch before selecting new work.
	rows, err := tx.QueryContext(ctx, `SELECT id::text,claim_token::text FROM kernel.webhook_deliveries WHERE status IN ('pending','failed') AND claim_token IS NOT NULL AND claimed_until <= $1 ORDER BY claimed_until,id LIMIT 100 FOR UPDATE SKIP LOCKED`, now)
	if err != nil {
		return nil, err
	}
	var expired []model.WebhookJob
	for rows.Next() {
		var job model.WebhookJob
		if err := rows.Scan(&job.ID, &job.ClaimToken); err != nil {
			rows.Close()
			return nil, err
		}
		expired = append(expired, job)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, job := range expired {
		if err := finishWebhook(ctx, tx, clock, job, 0, 0, false, "claim_expired"); err != nil {
			return nil, err
		}
	}
	var job model.WebhookJob
	err = tx.QueryRowContext(ctx, `SELECT d.id::text,d.endpoint_id::text,d.space_id::text,d.body,e.url,e.secret_ciphertext,e.secret_nonce,e.encryption_key_id FROM kernel.webhook_deliveries d JOIN kernel.webhook_endpoints e ON e.id=d.endpoint_id AND e.space_id=d.space_id WHERE d.status IN ('pending','failed') AND d.claim_token IS NULL AND d.next_attempt_at <= $1 AND e.status='active' ORDER BY d.next_attempt_at,d.id LIMIT 1 FOR UPDATE OF d SKIP LOCKED`, now).Scan(&job.ID, &job.EndpointID, &job.SpaceID, &job.Body, &job.URL, &job.Secret.Ciphertext, &job.Secret.Nonce, &job.Secret.KeyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim webhook: %w", err)
	}
	token, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	job.ClaimToken = token.String()
	_, err = tx.ExecContext(ctx, `UPDATE kernel.webhook_deliveries SET claim_token=$2,claimed_until=$3 WHERE id=$1`, job.ID, job.ClaimToken, now.Add(model.OutboxLease))
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// FinishWebhookDelivery records one observed result and releases its matching lease.
func FinishWebhookDelivery(ctx context.Context, tx *sql.Tx, clock model.Clock, job model.WebhookJob, status int, duration int64, success bool) error {
	outcome := "failed"
	if success {
		outcome = "sent"
	}
	return finishWebhook(ctx, tx, clock, job, status, duration, success, outcome)
}

func finishWebhook(ctx context.Context, tx *sql.Tx, clock model.Clock, job model.WebhookJob, status int, duration int64, success bool, outcome string) error {
	var attempt int
	var token sql.NullString
	var until sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT attempts,claim_token::text,claimed_until FROM kernel.webhook_deliveries WHERE id=$1 FOR UPDATE`, job.ID).Scan(&attempt, &token, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return model.NewError(model.CodeNotFound, "webhook delivery not found", nil)
	}
	if err != nil {
		return err
	}
	if !token.Valid || token.String != job.ClaimToken || (outcome != "claim_expired" && (!until.Valid || !until.Time.After(outboxTime(clock)))) {
		return model.NewError(model.CodeRuleViolation, "webhook delivery claim lost", nil)
	}
	attempt++
	next := outboxTime(clock)
	state := "failed"
	if success {
		state = "sent"
	} else if attempt >= model.OutboxMaxAttempts {
		state = "dead"
	} else {
		next = next.Add(model.OutboxBackoff(attempt))
	}
	var code any
	if status != 0 {
		code = status
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO kernel.webhook_delivery_attempts(id,space_id,delivery_id,attempt,outcome,http_status,duration_ms,created_at) VALUES($1,kernel.current_space_id(),$2,$3,$4,$5,$6,$7)`, id.String(), job.ID, attempt, outcome, code, duration, outboxTime(clock))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE kernel.webhook_deliveries SET attempts=$2,status=$3,next_attempt_at=$4,http_status=$5,duration_ms=$6,claim_token=NULL,claimed_until=NULL WHERE id=$1`, job.ID, attempt, state, next, code, duration)
	return err
}

// ListWebhookDeliveries exposes metadata only, after confirming endpoint ownership.
func ListWebhookDeliveries(ctx context.Context, tx *sql.Tx, endpoint string, limit int) ([]model.WebhookDelivery, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id::text FROM kernel.webhook_endpoints WHERE id=$1`, endpoint).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.NewError(model.CodeNotFound, "webhook endpoint not found", nil)
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text,endpoint_id::text,status,attempts,COALESCE(http_status,0),COALESCE(duration_ms,0) FROM kernel.webhook_deliveries WHERE endpoint_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2`, endpoint, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.WebhookDelivery{}
	for rows.Next() {
		var row model.WebhookDelivery
		if err := rows.Scan(&row.ID, &row.EndpointID, &row.Status, &row.Attempts, &row.HTTPStatus, &row.DurationMS); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// ListWebhookSpaces calls the fixed read-only directory, never a general unscoped query.
func ListWebhookSpaces(ctx context.Context, pool *sql.DB, after string, limit int) ([]string, error) {
	result := []string{}
	err := inTransaction(ctx, pool, "", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "SET TRANSACTION READ ONLY"); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT space_id::text FROM kernel.list_webhook_spaces($1,$2)`, nullIfEmpty(after), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			result = append(result, id)
		}
		return rows.Err()
	})
	return result, err
}

// FailWebhookOutbox records a local fanout failure without exposing its diagnostic data.
func FailWebhookOutbox(ctx context.Context, tx *sql.Tx, clock model.Clock, row model.OutboxRow) error {
	result, err := tx.ExecContext(ctx, `UPDATE kernel.outbox SET attempts=attempts+1,status=CASE WHEN attempts+1>=5 THEN 'dead' ELSE 'failed' END,last_error='webhook fanout failed',next_attempt_at=CASE WHEN attempts+1>=5 THEN next_attempt_at ELSE GREATEST(next_attempt_at,$3::timestamptz+($4::bigint[])[attempts+1]*interval '1 microsecond') END,claim_token=NULL,claimed_until=NULL WHERE event_seq=$1 AND subscriber='webhooks' AND claim_token=$2 AND claimed_until>$3 AND status IN ('pending','failed')`, row.EventSeq, row.ClaimToken, outboxTime(clock), backoffMicros())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return model.NewError(model.CodeRuleViolation, "webhook outbox claim lost", nil)
	}
	return nil
}
