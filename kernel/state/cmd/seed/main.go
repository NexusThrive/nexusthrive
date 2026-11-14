// Command seed creates a demo space for local development: three participants,
// a resource, a funded treasury and a person who may operate the ledger. It
// prints shell variable assignments for the API to read. It is a development
// tool: it needs a superuser DATABASE_URL to activate the space and it never
// runs against staging or production.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/nexusthrive/nexusthrive/kernel/db"
	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// subject is the Clerk-style subject of the demo operator.
const subject = "user_demo_ana"

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return fmt.Errorf("DATABASE_URL is empty")
	}
	admin, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("open admin pool: %w", err)
	}
	defer admin.Close()
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	api := sql.OpenDB(stdlib.GetConnector(*cfg, stdlib.OptionAfterConnect(func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE "+pgx.Identifier{db.APIRole}.Sanitize())
		return err
	})))
	defer api.Close()

	system := model.Actor{Kind: model.ActorSystem}
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("random suffix: %w", err)
	}
	space, err := state.CreateSpace(ctx, api, system, state.NewSpace{Kind: model.SpaceKindBusiness, Name: "Espacio de demostración", Slug: "demo-" + hex.EncodeToString(suffix)})
	if err != nil {
		return err
	}
	if _, err := admin.ExecContext(ctx, `UPDATE kernel.spaces SET status = 'active' WHERE id = $1`, space); err != nil {
		return fmt.Errorf("activate space: %w", err)
	}
	identity, err := state.CreateIdentity(ctx, api, system, state.NewIdentity{Provider: model.IdentityProviderClerk, Subject: subject + "_" + hex.EncodeToString(suffix), DisplayName: "Ana Torres"})
	if err != nil {
		return err
	}
	person := func(name, identityID string) (string, error) {
		return state.CreateParticipant(ctx, api, system, space, state.NewParticipant{Kind: model.ParticipantKindPerson, IdentityID: identityID, DisplayName: name})
	}
	ana, err := person("Ana Torres", identity)
	if err != nil {
		return err
	}
	luis, err := person("Luis Pérez", "")
	if err != nil {
		return err
	}
	treasury, err := state.CreateParticipant(ctx, api, system, space, state.NewParticipant{Kind: model.ParticipantKindOrganization, DisplayName: "Tesorería"})
	if err != nil {
		return err
	}
	resource, err := state.CreateResource(ctx, api, system, space, state.NewResource{Kind: model.ResourceKindMoney, Code: "COP", Name: "Peso colombiano", Scale: 2})
	if err != nil {
		return err
	}
	open := func(holder, code, name string, class model.AccountClass) (string, error) {
		return state.OpenAccount(ctx, api, system, space, state.NewAccount{HolderParticipantID: holder, ResourceID: resource, Class: class, Code: code, Name: name})
	}
	reserve, err := open(treasury, "RESERVE", "Reserva", model.AccountClassEquity)
	if err != nil {
		return err
	}
	anaWallet, err := open(ana, "WALLET", "Billetera de Ana", model.AccountClassAsset)
	if err != nil {
		return err
	}
	if _, err := open(luis, "WALLET", "Billetera de Luis", model.AccountClassAsset); err != nil {
		return err
	}
	role, err := state.CreateRole(ctx, api, system, space, model.NewRole{Key: "operator", Name: "Operador",
		Permissions: []string{model.PermissionExchangeWrite, model.PermissionLedgerRead, model.PermissionEventRead}})
	if err != nil {
		return err
	}
	if _, err := state.GrantRole(ctx, api, model.SystemClock{}, system, space, model.NewGrant{ParticipantID: ana, RoleID: role}); err != nil {
		return err
	}
	opening, err := model.ParseAmount("1000000.00")
	if err != nil {
		return err
	}
	if _, err := state.RecordExchange(ctx, api, model.Actor{Kind: model.ActorSystem, ParticipantID: ana}, space, model.NewExchange{
		Kind: "ledger.opening", IdempotencyKey: "seed-opening", Memo: "Saldo inicial",
		Entries: []model.NewEntry{{AccountID: anaWallet, ResourceID: resource, Amount: opening}, {AccountID: reserve, ResourceID: resource, Amount: opening.Neg()}},
	}); err != nil {
		return err
	}
	fmt.Printf("DEMO_SPACE_ID=%s\nDEMO_SUBJECT=%s\nDEMO_NAME=%s\n", space, subject+"_"+hex.EncodeToString(suffix), "Ana Torres")
	return nil
}
