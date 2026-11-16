package execution

import (
	"context"
	"strings"

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
	return e.run(ctx, call, "kernel.webhook.create", func(ctx context.Context, tx *Tx) error { return state.CreateWebhookEndpoint(ctx, tx.conn, in) })
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
