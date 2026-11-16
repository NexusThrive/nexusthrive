package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/nexusthrive/nexusthrive/kernel/execution"
	kernelv1 "github.com/nexusthrive/nexusthrive/kernel/gen/kernel/v1"
	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// WebhookGateway handles encrypted endpoint registration and internal HTTP delivery.
// Local mode is restricted to development wiring and literal loopback receivers.
type WebhookGateway struct {
	engine  *execution.Engine
	keys    *WebhookKeys
	network *webhookNetwork
	client  *http.Client
	clock   model.Clock
}

// NewWebhookGateway fails closed without explicit persistent encryption keys.
func NewWebhookGateway(engine *execution.Engine, keys *WebhookKeys, local bool) (*WebhookGateway, error) {
	if engine == nil || keys == nil {
		return nil, errors.New("webhooks need an engine and persistent encryption keys")
	}
	network := newWebhookNetwork(local)
	return &WebhookGateway{engine: engine, keys: keys, network: network, client: network.client(), clock: model.SystemClock{}}, nil
}

// DispatchSpace performs one bounded space pass. The scheduler controls global fairness.
func (w *WebhookGateway) DispatchSpace(ctx context.Context, space string) error {
	call := execution.Call{Actor: model.Actor{Kind: model.ActorSystem}, SpaceID: space, RequestID: execution.NewRequestID()}
	if err := w.engine.FanoutWebhooks(ctx, call, 100); err != nil {
		return err
	}
	job, err := w.engine.ClaimWebhookDelivery(ctx, call)
	if err != nil || job == nil {
		return err
	}
	secret, err := w.keys.Open(space, job.EndpointID, job.Secret)
	if err != nil {
		return err
	}
	if err := w.network.Validate(ctx, job.URL); err != nil {
		return w.engine.FinishWebhookDelivery(ctx, call, *job, 0, 0, false, false)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, job.URL, bytes.NewReader(job.Body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("NexusThrive-Signature", WebhookSignature(w.clock.Now(), job.Body, secret))
	started := time.Now()
	response, sendErr := w.client.Do(request)
	duration := time.Since(started).Milliseconds()
	code := 0
	if response != nil {
		code = response.StatusCode
		// Drain only a bounded amount; response content is never persisted or logged.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if code < 100 || code > 599 {
		code = 0
	}
	success := sendErr == nil && code >= 200 && code < 300
	// Redirect failures can carry the redirect's status; they remain failed attempts.
	return w.engine.FinishWebhookDelivery(ctx, call, *job, code, duration, success, !errors.Is(sendErr, errWebhookDestination))
}

// CreateWebhookEndpoint presents the new secret once and never stores a replay response.
func (s *ConnectServer) CreateWebhookEndpoint(ctx context.Context, request *connect.Request[kernelv1.CreateWebhookEndpointRequest]) (*connect.Response[kernelv1.CreateWebhookEndpointResponse], error) {
	call, ok := ctx.Value(rpcCallKey{}).(execution.Call)
	if !ok || s.cfg.Webhooks == nil {
		return nil, s.rpcError(errors.New("webhooks are not configured"))
	}
	if call.IdempotencyKey != "" {
		return nil, s.rpcError(model.NewError(model.CodeInvalidInput, "webhook secret creation does not support idempotency keys", nil))
	}
	hooks := s.cfg.Webhooks
	if err := hooks.network.Validate(ctx, request.Msg.GetUrl()); err != nil {
		return nil, s.rpcError(err)
	}
	id := execution.NewRequestID()
	sealed, secret, err := hooks.keys.NewSecret(call.SpaceID, id)
	if err != nil {
		return nil, s.rpcError(err)
	}
	err = s.engine.CreateWebhookEndpoint(ctx, call, model.NewWebhookEndpoint{ID: id, URL: request.Msg.GetUrl(), Events: request.Msg.GetEvents(), Secret: sealed})
	if err != nil {
		return nil, s.rpcError(err)
	}
	result := connect.NewResponse(&kernelv1.CreateWebhookEndpointResponse{Id: id, Secret: secret})
	result.Header().Set("Cache-Control", "no-store")
	return result, nil
}

// ListWebhookDeliveries exposes delivery metadata under its read permission.
func (s *ConnectServer) ListWebhookDeliveries(ctx context.Context, request *connect.Request[kernelv1.ListWebhookDeliveriesRequest]) (*connect.Response[kernelv1.ListWebhookDeliveriesResponse], error) {
	call, ok := ctx.Value(rpcCallKey{}).(execution.Call)
	if !ok {
		return nil, s.rpcError(errors.New("missing verified call"))
	}
	rows, err := s.engine.ListWebhookDeliveries(ctx, call, request.Msg.GetEndpointId(), int(request.Msg.GetLimit()))
	if err != nil {
		return nil, s.rpcError(err)
	}
	result := &kernelv1.ListWebhookDeliveriesResponse{}
	for _, row := range rows {
		result.Deliveries = append(result.Deliveries, &kernelv1.WebhookDelivery{Id: row.ID, EndpointId: row.EndpointID, Status: row.Status, Attempts: int32(row.Attempts), HttpStatus: int32(row.HTTPStatus), DurationMs: row.DurationMS, FailureReason: row.FailureReason})
	}
	return connect.NewResponse(result), nil
}

// DispatchPage rotates over a bounded directory page and continues after space-local failures.
// The returned cursor advances only through spaces actually attempted; the API scheduler
// owns pass serialization and its global deadline.
func (w *WebhookGateway) DispatchPage(ctx context.Context, after string) (string, error) {
	actor := model.Actor{Kind: model.ActorSystem}
	spaces, err := w.engine.ListWebhookSpaces(ctx, actor, after, 10)
	if err != nil {
		return after, err
	}
	if len(spaces) == 0 && after != "" {
		spaces, err = w.engine.ListWebhookSpaces(ctx, actor, "", 10)
		if err != nil {
			return after, err
		}
	}
	var failures []error
	for _, space := range spaces {
		if ctx.Err() != nil {
			return after, ctx.Err()
		}
		after = space
		if err := w.DispatchSpace(ctx, space); err != nil {
			failures = append(failures, err)
		}
	}
	return after, errors.Join(failures...)
}
