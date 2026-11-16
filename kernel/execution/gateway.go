package execution

import (
	"context"
	"database/sql"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// ResolveActor turns an identity that a gateway has already verified into the
// actor that acts in spaceID. It is the only door from a validated token to an
// Actor (ADR-0035): an identity with no active participant in the space is
// permission_denied, with one message for every cause so a caller learns
// nothing about which spaces or identities exist.
func (e *Engine) ResolveActor(ctx context.Context, spaceID string, provider model.IdentityProvider, subject string) (model.Actor, error) {
	denied := model.NewError(model.CodePermissionDenied, "the identity has no access to this space", nil)
	if !provider.Valid() || subject == "" {
		return model.Actor{}, denied
	}
	var participantID string
	var found bool
	err := e.guarded("gateway.resolve_actor", func() error {
		return state.InSpace(ctx, e.pool, spaceID, func(ctx context.Context, conn *sql.Tx) error {
			var err error
			participantID, found, err = state.FindParticipantByIdentity(ctx, conn, provider, subject)
			return err
		})
	})
	if err != nil {
		return model.Actor{}, err
	}
	if !found {
		return model.Actor{}, denied
	}
	return model.Actor{Kind: model.ActorPerson, ParticipantID: participantID}, nil
}

// ListBalances returns the accounts of the space with their balances. The
// operation must have declared kernel.ledger.read.
func (t *Tx) ListBalances(ctx context.Context) ([]model.AccountBalance, error) {
	if err := t.begin(); err != nil {
		return nil, err
	}
	defer t.life.mu.Unlock()
	if err := t.requireDeclared(model.PermissionLedgerRead); err != nil {
		return nil, err
	}
	return state.ListAccountBalances(ctx, t.conn)
}
