package gateway_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

const subjectOperator = "user_operator"

// seed creates an active space with an operator who may run the ledger, an
// account with funds and an empty one. It returns the space and the two
// accounts.
func seed(t *testing.T, admin, api *sql.DB) (space, funded, empty string) {
	t.Helper()
	ctx := context.Background()
	system := model.Actor{Kind: model.ActorSystem}
	must := func(id string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	space = must(state.CreateSpace(ctx, api, system, state.NewSpace{Kind: model.SpaceKindBusiness, Name: "t", Slug: "t"}))
	if _, err := admin.Exec(`UPDATE kernel.spaces SET status = 'active' WHERE id = $1`, space); err != nil {
		t.Fatal(err)
	}
	identity := must(state.CreateIdentity(ctx, api, system, state.NewIdentity{Provider: model.IdentityProviderClerk, Subject: subjectOperator, DisplayName: "Op"}))
	op := must(state.CreateParticipant(ctx, api, system, space, state.NewParticipant{Kind: model.ParticipantKindPerson, IdentityID: identity, DisplayName: "Op"}))
	peer := must(state.CreateParticipant(ctx, api, system, space, state.NewParticipant{Kind: model.ParticipantKindPerson, DisplayName: "Peer"}))
	resource := must(state.CreateResource(ctx, api, system, space, state.NewResource{Kind: model.ResourceKindMoney, Code: "COP", Name: "Peso", Scale: 2}))
	open := func(holder, code string) string {
		return must(state.OpenAccount(ctx, api, system, space, state.NewAccount{HolderParticipantID: holder, ResourceID: resource, Class: model.AccountClassAsset, Code: code, Name: code}))
	}
	funded, empty = open(op, "A"), open(peer, "B")
	reserve := open(peer, "RESERVE")
	role := must(state.CreateRole(ctx, api, system, space, model.NewRole{Key: "op", Name: "op",
		Permissions: []string{model.PermissionExchangeWrite, model.PermissionLedgerRead, model.PermissionEventRead}}))
	must(state.GrantRole(ctx, api, model.SystemClock{}, system, space, model.NewGrant{ParticipantID: op, RoleID: role}))
	amount, _ := model.ParseAmount("1000.00")
	if _, err := state.RecordExchange(ctx, api, model.Actor{Kind: model.ActorSystem, ParticipantID: op}, space, model.NewExchange{Kind: "ledger.opening", IdempotencyKey: "open",
		Entries: []model.NewEntry{{AccountID: funded, ResourceID: resource, Amount: amount}, {AccountID: reserve, ResourceID: resource, Amount: amount.Neg()}}}); err != nil {
		t.Fatal(err)
	}
	return space, funded, empty
}
