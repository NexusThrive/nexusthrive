package execution

import (
	"context"
	"regexp"
	"strings"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

var directorySlugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ResolvedActor is a server-resolved identity and its active space participant.
type ResolvedActor struct {
	SpaceID    string
	IdentityID string
	Actor      model.Actor
}

// ResolveActorForSlug resolves only an authenticated Clerk subject. A successful
// lookup does not authorize an operation: its transaction must check permissions again.
func (e *Engine) ResolveActorForSlug(ctx context.Context, slug string, provider model.IdentityProvider, subject string) (ResolvedActor, error) {
	denied := model.NewError(model.CodePermissionDenied, "the identity has no access to this space", nil)
	if provider != model.IdentityProviderClerk || strings.TrimSpace(subject) == "" || len(slug) > 63 || !directorySlugPattern.MatchString(slug) {
		return ResolvedActor{}, denied
	}
	var result ResolvedActor
	var participant string
	var found bool
	err := e.guarded("gateway.resolve_actor", func() error {
		var err error
		result.SpaceID, result.IdentityID, participant, found, err = state.LookupSpaceActor(ctx, e.pool, string(provider), subject, slug)
		return err
	})
	if err != nil {
		return ResolvedActor{}, err
	}
	if !found {
		return ResolvedActor{}, denied
	}
	result.Actor = model.Actor{Kind: model.ActorPerson, ParticipantID: participant}
	return result, nil
}
