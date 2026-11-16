package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/nexusthrive/nexusthrive/kernel/execution"
	kernelv1 "github.com/nexusthrive/nexusthrive/kernel/gen/kernel/v1"
	"github.com/nexusthrive/nexusthrive/kernel/gen/kernel/v1/kernelv1connect"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AuthenticatedCall is authority resolved by a trusted authenticator, never a request body.
type AuthenticatedCall struct {
	Actor        model.Actor
	SpaceID      string
	CredentialID string
}

// AuthenticateConnect verifies a credential and resolves its participant in the slug.
type AuthenticateConnect func(context.Context, *http.Request, string) (AuthenticatedCall, error)

// ConnectConfig controls bounded in-memory admission and operation deadlines.
type ConnectConfig struct {
	// Webhooks enables only the implemented webhook operations. Nil keeps them hidden.
	Webhooks          *WebhookGateway
	Timeout           time.Duration
	RequestsPerMinute int
	MaxClients        int
	Clock             func() time.Time
	Log               *slog.Logger
}

type rpcCallKey struct{}

// ConnectServer exposes the generated service using only the execution engine.
type ConnectServer struct {
	engine  *execution.Engine
	auth    AuthenticateConnect
	cfg     ConnectConfig
	handler http.Handler
	writer  *connect.ErrorWriter
	mu      sync.Mutex
	window  time.Time
	counts  map[string]int
}

// NewConnectServer requires an authenticator: there is no anonymous fallback.
func NewConnectServer(engine *execution.Engine, auth AuthenticateConnect, cfg ConnectConfig) (*ConnectServer, error) {
	if engine == nil || auth == nil {
		return nil, errors.New("connect needs an engine and an authenticator")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.RequestsPerMinute == 0 {
		cfg.RequestsPerMinute = 120
	}
	if cfg.MaxClients == 0 {
		cfg.MaxClients = 10000
	}
	if cfg.Timeout < 0 || cfg.RequestsPerMinute < 0 || cfg.MaxClients < 0 {
		return nil, errors.New("connect limits must be positive")
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	s := &ConnectServer{engine: engine, auth: auth, cfg: cfg, counts: map[string]int{}, writer: connect.NewErrorWriter()}
	_, s.handler = kernelv1connect.NewKernelServiceHandler(s, connect.WithReadMaxBytes(1<<20))
	return s, nil
}

// ServeHTTP bounds work before authentication and preserves the original money body.
func (s *ConnectServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if value := recover(); value != nil {
			s.cfg.Log.Error("gateway panic", "value", value)
			_ = s.writer.Write(w, r, connect.NewError(connect.CodeInternal, errors.New("internal error")))
		}
	}()
	slug, path, found := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	path = "/" + path
	if !found || slug == "" || (path != kernelv1connect.KernelServiceWriteExchangeProcedure && path != kernelv1connect.KernelServiceReadEventsProcedure && !(s.cfg.Webhooks != nil && (path == kernelv1connect.KernelServiceCreateWebhookEndpointProcedure || path == kernelv1connect.KernelServiceListWebhookDeliveriesProcedure))) {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Timeout)
	defer cancel()
	r = r.WithContext(ctx)
	refuse := func(err error) { _ = s.writer.Write(w, r, s.rpcError(err)) }
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if wait := s.admit("ip:" + ip); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		refuse(connect.NewError(connect.CodeResourceExhausted, errors.New("request limit exceeded")))
		return
	}
	identity, err := s.auth(ctx, r, slug)
	if err != nil {
		refuse(err)
		return
	}
	if identity.Actor.IsNil() || identity.Actor.Kind == model.ActorSystem || identity.SpaceID == "" || identity.CredentialID == "" {
		refuse(connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required")))
		return
	}
	if wait := s.admit("credential:" + identity.CredentialID); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		refuse(connect.NewError(connect.CodeResourceExhausted, errors.New("request limit exceeded")))
		return
	}
	if path == kernelv1connect.KernelServiceWriteExchangeProcedure {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		encoding := r.Header.Get("Content-Encoding")
		if err != nil || mediaType != "application/json" || (encoding != "" && encoding != "identity") {
			refuse(model.NewError(model.CodeInvalidInput, "money requests require uncompressed Connect JSON", nil))
			return
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		refuse(model.NewError(model.CodeInvalidInput, "request body is unreadable or too large", nil))
		return
	}
	call := execution.Call{Actor: identity.Actor, SpaceID: identity.SpaceID, RequestID: execution.NewRequestID(), IdempotencyKey: r.Header.Get("Idempotency-Key"), Request: body}
	w.Header().Set("X-Request-Id", call.RequestID)
	r = r.WithContext(context.WithValue(ctx, rpcCallKey{}, call))
	r.Body = io.NopCloser(bytes.NewReader(body))
	cloned := *r.URL
	cloned.Path = path
	cloned.RawPath = ""
	r.URL = &cloned
	s.handler.ServeHTTP(w, r)
}

// admit caps both counters and distinct clients for one fixed minute window.
func (s *ConnectServer) admit(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.cfg.Clock()
	if s.window.IsZero() || now.Sub(s.window) >= time.Minute {
		s.window = now
		clear(s.counts)
	}
	_, exists := s.counts[key]
	if s.counts[key] >= s.cfg.RequestsPerMinute || (!exists && len(s.counts) >= s.cfg.MaxClients) {
		wait := int((time.Minute - now.Sub(s.window) + time.Second - 1) / time.Second)
		if wait < 1 {
			wait = 1
		}
		return wait
	}
	s.counts[key]++
	return 0
}

// WriteExchange forwards the captured body, not a re-encoded protobuf request.
func (s *ConnectServer) WriteExchange(ctx context.Context, _ *connect.Request[kernelv1.WriteExchangeRequest]) (*connect.Response[kernelv1.WriteExchangeResponse], error) {
	call, ok := ctx.Value(rpcCallKey{}).(execution.Call)
	if !ok {
		return nil, s.rpcError(errors.New("missing verified call"))
	}
	result, err := s.engine.WriteExchange(ctx, call)
	if err != nil {
		return nil, s.rpcError(err)
	}
	message := new(kernelv1.WriteExchangeResponse)
	if err := protojson.Unmarshal(result.Body, message); err != nil {
		return nil, s.rpcError(err)
	}
	response := connect.NewResponse(message)
	if result.Replayed {
		response.Header().Set("Idempotent-Replayed", "true")
	}
	return response, nil
}

// ReadEvents translates committed model events to the generated response.
func (s *ConnectServer) ReadEvents(ctx context.Context, request *connect.Request[kernelv1.ReadEventsRequest]) (*connect.Response[kernelv1.ReadEventsResponse], error) {
	call, ok := ctx.Value(rpcCallKey{}).(execution.Call)
	if !ok {
		return nil, s.rpcError(errors.New("missing verified call"))
	}
	events, err := s.engine.ReadEvents(ctx, call, request.Msg.GetFromSeq(), int(request.Msg.GetLimit()))
	if err != nil {
		return nil, s.rpcError(err)
	}
	message := &kernelv1.ReadEventsResponse{}
	for _, event := range events {
		payload := new(structpb.Struct)
		if err := protojson.Unmarshal(event.Payload, payload); err != nil {
			return nil, s.rpcError(err)
		}
		message.Events = append(message.Events, &kernelv1.Event{Id: event.ID, Seq: event.Seq, SpaceId: event.SpaceID, Type: event.Type, Version: int32(event.Version), SubjectRef: event.SubjectRef, Payload: payload, ActorKind: string(event.Actor.Kind), ActorParticipantId: event.Actor.ParticipantID, RequestId: event.RequestID, OccurredAt: timestamppb.New(event.OccurredAt)})
	}
	return connect.NewResponse(message), nil
}

func (s *ConnectServer) rpcError(err error) error {
	var typed *model.Error
	if errors.As(err, &typed) {
		codes := map[model.Code]connect.Code{model.CodeInvalidInput: connect.CodeInvalidArgument, model.CodePermissionDenied: connect.CodePermissionDenied, model.CodeNotFound: connect.CodeNotFound, model.CodeAlreadyDone: connect.CodeAlreadyExists, model.CodeIdempotencyConflict: connect.CodeAlreadyExists, model.CodeRuleViolation: connect.CodeFailedPrecondition, model.CodeSpaceSuspended: connect.CodeFailedPrecondition, model.CodeCapabilityInactive: connect.CodeFailedPrecondition, model.CodeRetryable: connect.CodeAborted}
		if code, ok := codes[typed.Code]; ok {
			failure := connect.NewError(code, errors.New(typed.Message))
			if detail, err := connect.NewErrorDetail(&kernelv1.OperationError{Code: kernelv1.ErrorCode(kernelv1.ErrorCode_value["ERROR_CODE_"+strings.ToUpper(string(typed.Code))]), Message: typed.Message}); err == nil {
				failure.AddDetail(detail)
			}
			return failure
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(connect.CodeDeadlineExceeded, errors.New("request deadline exceeded"))
	}
	if errors.Is(err, context.Canceled) {
		return connect.NewError(connect.CodeCanceled, errors.New("request canceled"))
	}
	var transport *connect.Error
	if errors.As(err, &transport) && (transport.Code() == connect.CodeUnauthenticated || transport.Code() == connect.CodeResourceExhausted) {
		return connect.NewError(transport.Code(), errors.New("request refused"))
	}
	s.cfg.Log.Error("operation failed", "error", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}
