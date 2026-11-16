package execution

import (
	"context"
	"encoding/json"
	kernelv1 "github.com/nexusthrive/nexusthrive/kernel/gen/kernel/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// CreateWebhookEndpoint stores a presealed secret without caching its presentation.
func (e *Engine) CreateWebhookEndpoint(ctx context.Context, call Call, in model.NewWebhookEndpoint) error {
	id, err := uuid.Parse(in.ID)
	if err != nil || id.Version() != 7 || len(in.URL) == 0 || len(in.URL) > 2048 || len(in.Events) < 1 || len(in.Events) > 100 || len(in.Secret.Ciphertext) != 48 || len(in.Secret.Nonce) != 12 || len(in.Secret.KeyID) < 1 || len(in.Secret.KeyID) > 128 {
		return model.NewError(model.CodeInvalidInput, "invalid webhook endpoint", nil)
	}
	seen := map[string]bool{}
	for _, name := range in.Events {
		if seen[name] || (name != "kernel.exchange.recorded.v1" && name != "kernel.commitment.expired.v1") {
			return model.NewError(model.CodeInvalidInput, "invalid webhook event filter", nil)
		}
		seen[name] = true
	}
	return e.run(ctx, call, "kernel.webhook.create", func(ctx context.Context, tx *Tx) error {
		if err := state.CreateWebhookEndpoint(ctx, tx.conn, in); err != nil {
			return err
		}
		return webhookAudit(ctx, tx, "kernel.webhook.endpoint.created", map[string]string{"endpoint_id": in.ID})
	})
}

// ListWebhookDeliveries reads metadata without ever returning the encrypted signing key.
func (e *Engine) ListWebhookDeliveries(ctx context.Context, call Call, endpoint string, limit int) ([]model.WebhookDelivery, error) {
	if id, err := uuid.Parse(endpoint); err != nil || id == uuid.Nil || limit < 1 || limit > 100 {
		return nil, model.NewError(model.CodeInvalidInput, "invalid webhook delivery page", nil)
	}
	var result []model.WebhookDelivery
	err := e.run(ctx, call, "kernel.webhook.deliveries", func(ctx context.Context, tx *Tx) error {
		var err error
		result, err = state.ListWebhookDeliveries(ctx, tx.conn, endpoint, limit)
		return err
	})
	return result, err
}

func requireWebhookWorker(call Call) error {
	if call.Actor.Kind != model.ActorSystem || strings.TrimSpace(call.Actor.ParticipantID) != "" {
		return model.NewError(model.CodePermissionDenied, "webhook work requires the internal system actor", nil)
	}
	return nil
}

// FanoutWebhooks durably hands claimed events to their matching endpoint queues.
func (e *Engine) FanoutWebhooks(ctx context.Context, call Call, limit int) error {
	if err := requireWebhookWorker(call); err != nil {
		return err
	}
	rows, err := e.ClaimOutbox(ctx, call, model.SubscriberWebhooks, limit)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := e.run(ctx, call, operationOutboxClaim, func(ctx context.Context, tx *Tx) error { return state.FanoutWebhook(ctx, tx.conn, tx.clock, row) }); err != nil {
			// A canceled or suspended call leaves its lease recoverable. Otherwise
			// persist the fanout failure immediately, with no receiver attribution.
			if ctx.Err() == nil {
				_ = e.run(ctx, call, operationOutboxClaim, func(ctx context.Context, tx *Tx) error { return state.FailWebhookOutbox(ctx, tx.conn, tx.clock, row) })
			}
			return err
		}
	}
	return nil
}

// ClaimWebhookDelivery is an internal fixed handler; gateways never expose it.
func (e *Engine) ClaimWebhookDelivery(ctx context.Context, call Call) (*model.WebhookJob, error) {
	if err := requireWebhookWorker(call); err != nil {
		return nil, err
	}
	var job *model.WebhookJob
	err := e.run(ctx, call, operationOutboxClaim, func(ctx context.Context, tx *Tx) error {
		var err error
		job, err = state.ClaimWebhookDelivery(ctx, tx.conn, tx.clock)
		return err
	})
	return job, err
}

// FinishWebhookDelivery binds the result to the lease and current space.
func (e *Engine) FinishWebhookDelivery(ctx context.Context, call Call, job model.WebhookJob, status int, duration int64, success, observed bool) error {
	if err := requireWebhookWorker(call); err != nil {
		return err
	}
	if id, err := uuid.Parse(job.ID); err != nil || id == uuid.Nil {
		return model.NewError(model.CodeInvalidInput, "invalid webhook delivery", nil)
	}
	if id, err := uuid.Parse(job.ClaimToken); err != nil || id == uuid.Nil || duration < 0 || status < 0 || (status > 0 && status < 100) || status > 599 || success != (status >= 200 && status < 300) || (success && !observed) {
		return model.NewError(model.CodeInvalidInput, "invalid webhook result", nil)
	}
	return e.run(ctx, call, operationOutboxClaim, func(ctx context.Context, tx *Tx) error {
		return state.FinishWebhookDelivery(ctx, tx.conn, tx.clock, job, status, duration, success, observed)
	})
}

// ListWebhookSpaces exposes only bounded active-space IDs to the internal worker.
func (e *Engine) ListWebhookSpaces(ctx context.Context, actor model.Actor, after string, limit int) ([]string, error) {
	if err := requireWebhookWorker(Call{Actor: actor}); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 10 {
		return nil, model.NewError(model.CodeInvalidInput, "invalid worker directory page", nil)
	}
	if after != "" {
		if id, err := uuid.Parse(after); err != nil || id == uuid.Nil {
			return nil, model.NewError(model.CodeInvalidInput, "invalid worker directory cursor", nil)
		}
	}
	var result []string
	err := e.guarded("kernel.webhook.spaces", func() error {
		var err error
		result, err = state.ListWebhookSpaces(ctx, e.pool, after, limit)
		return err
	})
	return result, err
}

// RotateWebhookSecret atomically replaces the encrypted key and records a secret-free audit event.
func (e *Engine) RotateWebhookSecret(ctx context.Context, call Call, endpoint string, secret model.SealedWebhookSecret) (time.Time, error) {
	if id, err := uuid.Parse(endpoint); err != nil || id == uuid.Nil || len(secret.Ciphertext) != 48 || len(secret.Nonce) != 12 || len(secret.KeyID) < 1 || len(secret.KeyID) > 128 {
		return time.Time{}, model.NewError(model.CodeInvalidInput, "invalid webhook rotation", nil)
	}
	var until time.Time
	err := e.run(ctx, call, "kernel.webhook.rotate", func(ctx context.Context, tx *Tx) error {
		var err error
		until, err = state.RotateWebhookSecret(ctx, tx.conn, tx.clock, endpoint, secret)
		if err != nil {
			return err
		}
		return webhookAudit(ctx, tx, "kernel.webhook.secret.rotated", map[string]string{"endpoint_id": endpoint})
	})
	if err != nil {
		return time.Time{}, err
	}
	return until, nil
}

// ReplayWebhookDelivery fingerprints the original request and fixes the source selected by its body.
func (e *Engine) ReplayWebhookDelivery(ctx context.Context, call Call) (Response, error) {
	if _, err := model.CanonicalJSON(call.Request); err != nil {
		return Response{}, err
	}
	var request kernelv1.ReplayWebhookDeliveryRequest
	if err := protojson.Unmarshal(call.Request, &request); err != nil {
		return Response{}, model.NewError(model.CodeInvalidInput, "invalid webhook replay request", nil)
	}
	if id, err := uuid.Parse(request.GetDeliveryId()); err != nil || id == uuid.Nil {
		return Response{}, model.NewError(model.CodeInvalidInput, "invalid webhook delivery", nil)
	}
	if key := request.GetIdempotencyKey(); key != "" {
		if call.IdempotencyKey != "" && call.IdempotencyKey != key {
			return Response{}, model.NewError(model.CodeInvalidInput, "idempotency keys must match", nil)
		}
		call.IdempotencyKey = key
	}
	return e.runIdempotent(ctx, call, "kernel.webhook.replay", func(ctx context.Context, tx *Tx) (json.RawMessage, error) {
		id, err := state.ReplayWebhookDelivery(ctx, tx.conn, tx.clock, request.GetDeliveryId())
		if err != nil {
			return nil, err
		}
		if err := webhookAudit(ctx, tx, "kernel.webhook.delivery.replayed", map[string]string{"delivery_id": id, "source_delivery_id": request.GetDeliveryId()}); err != nil {
			return nil, err
		}
		return protojson.Marshal(&kernelv1.ReplayWebhookDeliveryResponse{DeliveryId: id})
	})
}

func webhookAudit(ctx context.Context, tx *Tx, kind string, payload map[string]string) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = state.AppendEvent(ctx, tx.conn, model.NewEvent{Type: kind, Version: 1, Payload: raw, Actor: tx.call.Actor, RequestID: tx.call.RequestID})
	return err
}
