package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/nexusthrive/nexusthrive/kernel/execution"
	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// NewClerkAuthenticator accepts authority only from a verified token and the
// engine's participant directory, never from request fields or asserted UUIDs.
func NewClerkAuthenticator(engine *execution.Engine, verifier *Verifier) (AuthenticateConnect, error) {
	if engine == nil || verifier == nil {
		return nil, errors.New("a Clerk authenticator needs an engine and verifier")
	}
	return func(ctx context.Context, r *http.Request, slug string) (AuthenticatedCall, error) {
		token, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !found {
			return AuthenticatedCall{}, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
		}
		claims, err := verifier.Verify(ctx, token)
		if err != nil {
			if ctx.Err() != nil {
				return AuthenticatedCall{}, ctx.Err()
			}
			return AuthenticatedCall{}, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
		}
		resolved, err := engine.ResolveActorForSlug(ctx, slug, model.IdentityProviderClerk, claims.Subject)
		if err != nil {
			return AuthenticatedCall{}, err
		}
		return AuthenticatedCall{Actor: resolved.Actor, SpaceID: resolved.SpaceID, CredentialID: "clerk:" + resolved.IdentityID}, nil
	}, nil
}
