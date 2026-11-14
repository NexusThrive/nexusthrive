package gateway

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/model"
)

//go:embed console.html
var consolePage []byte

// maxBodyBytes bounds a request body: nothing the gateway accepts is larger.
const maxBodyBytes = 64 << 10

// Dev wires the local development helpers: a token issuer, and the space and
// identity the console page offers. A nil Dev leaves them out, which is the
// production setting.
type Dev struct {
	Issuer  *DevIssuer
	SpaceID string
	Subject string
	Name    string
}

// Server is the HTTP handler of the gateway.
type Server struct {
	engine   *execution.Engine
	verifier *Verifier
	dev      *Dev
	log      *slog.Logger
	mux      *http.ServeMux
}

// NewServer builds the handler. It receives the engine and nothing under it.
func NewServer(engine *execution.Engine, verifier *Verifier, dev *Dev, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{engine: engine, verifier: verifier, dev: dev, log: log, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("GET /v1/spaces/{space}/balances", s.balances)
	s.mux.HandleFunc("GET /v1/spaces/{space}/events", s.events)
	s.mux.HandleFunc("POST /v1/spaces/{space}/transfers", s.transfer)
	if dev != nil {
		s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'")
			_, _ = w.Write(consolePage)
		})
		s.mux.HandleFunc("GET /dev/session", s.devSession)
		s.mux.HandleFunc("GET /.well-known/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(dev.Issuer.JWKS())
		})
	}
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// devSession hands the console page a short token for the demo identity.
func (s *Server) devSession(w http.ResponseWriter, _ *http.Request) {
	token, err := s.dev.Issuer.Token(s.dev.Subject, time.Now().Add(time.Hour))
	if err != nil {
		s.fail(w, "", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token, "space_id": s.dev.SpaceID, "name": s.dev.Name})
}

// authenticate verifies the bearer token and resolves the actor in the space of
// the path. It writes the refusal itself and returns false when it fails.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (execution.Call, bool) {
	token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return execution.Call{}, false
	}
	claims, err := s.verifier.Verify(r.Context(), token)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeError(w, http.StatusUnauthorized, "unauthenticated", "the token is not valid")
		return execution.Call{}, false
	}
	space := r.PathValue("space")
	actor, err := s.engine.ResolveActor(r.Context(), space, model.IdentityProviderClerk, claims.Subject)
	if err != nil {
		s.fail(w, "", err)
		return execution.Call{}, false
	}
	requestID := execution.NewRequestID()
	w.Header().Set("X-Request-Id", requestID)
	return execution.Call{Actor: actor, SpaceID: space, RequestID: requestID}, true
}

func (s *Server) balances(w http.ResponseWriter, r *http.Request) {
	call, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var out []model.AccountBalance
	err := s.engine.Run(r.Context(), call, opBalances, func(ctx context.Context, tx *execution.Tx) error {
		var err error
		out, err = tx.ListBalances(ctx)
		return err
	})
	if err != nil {
		s.fail(w, call.RequestID, err)
		return
	}
	if out == nil {
		out = []model.AccountBalance{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	call, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	from, _ := strconv.ParseInt(r.URL.Query().Get("from_seq"), 10, 64)
	var out []eventView
	err := s.engine.Run(r.Context(), call, opEvents, func(ctx context.Context, tx *execution.Tx) error {
		events, err := tx.ReadEvents(ctx, from, 100)
		for _, e := range events {
			out = append(out, eventView{Seq: e.Seq, Type: e.Type, OccurredAt: e.OccurredAt, ActorKind: string(e.Actor.Kind),
				ActorID: e.Actor.ParticipantID, RequestID: e.RequestID, Payload: e.Payload})
		}
		return err
	})
	if err != nil {
		s.fail(w, call.RequestID, err)
		return
	}
	if out == nil {
		out = []eventView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

// transfer moves value. The Idempotency-Key header is required: repeating the
// request with the same key and body returns the stored response and marks it
// with Idempotent-Replayed.
func (s *Server) transfer(w http.ResponseWriter, r *http.Request) {
	call, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		s.fail(w, call.RequestID, model.NewError(model.CodeInvalidInput, "the body is unreadable or too large", nil))
		return
	}
	var req transferRequest
	if err := decodeStrict(raw, &req); err != nil {
		s.fail(w, call.RequestID, model.NewError(model.CodeInvalidInput, "the body must be a JSON object with from_account_id, to_account_id, amount and memo", nil))
		return
	}
	amount, err := model.ParseAmount(req.Amount)
	if err != nil || amount.Sign() <= 0 {
		s.fail(w, call.RequestID, model.NewError(model.CodeInvalidInput, "amount must be a positive decimal", nil))
		return
	}
	call.IdempotencyKey = r.Header.Get("Idempotency-Key")
	call.Request = raw
	response, err := s.engine.RunIdempotent(r.Context(), call, opTransfer, transferHandler(req, amount))
	if err != nil {
		s.fail(w, call.RequestID, err)
		return
	}
	if response.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response.Body)
}

// decodeStrict rejects unknown fields and trailing data.
func decodeStrict(raw []byte, into any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}

// fail writes the contract error for err. Typed errors keep their message; any
// other error, a recovered panic included, is logged and answered with a
// generic 500 that tells the caller nothing of the inside.
func (s *Server) fail(w http.ResponseWriter, requestID string, err error) {
	if code, typed := model.CodeOf(err); typed {
		var typedErr *model.Error
		_ = errors.As(err, &typedErr)
		writeError(w, statusOf(code), string(code), typedErr.Message)
		return
	}
	s.log.Error("request failed", "request_id", requestID, "error", err.Error())
	writeError(w, http.StatusInternalServerError, "internal", "internal error")
}

func statusOf(code model.Code) int {
	switch code {
	case model.CodeInvalidInput:
		return http.StatusBadRequest
	case model.CodeNotFound:
		return http.StatusNotFound
	case model.CodePermissionDenied, model.CodeCapabilityInactive, model.CodeSpaceSuspended:
		return http.StatusForbidden
	case model.CodeAlreadyDone, model.CodeIdempotencyConflict:
		return http.StatusConflict
	case model.CodeRuleViolation:
		return http.StatusUnprocessableEntity
	}
	return http.StatusInternalServerError
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
