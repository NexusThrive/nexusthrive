package laws_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// isolationWorld is a database with two spaces that hold a row in every table
// the tests know how to fill, plus identities that are visible from one space
// only and from both.
type isolationWorld struct {
	*engineWorld
	// ownIdentity maps a space to the identity linked only there.
	ownIdentity map[string]string
	// sharedIdentity is linked in both spaces.
	sharedIdentity string
}

// newIsolationWorld fills both spaces of an engine world: participants,
// roles, grants, a resource with accounts, an exchange with entries, a
// commitment with its timer and a fulfillment, an event with its outbox row,
// an active capability and an access audit entry. Tables added by later
// stories are filled by fillMissingTables.
func newIsolationWorld(t *testing.T) *isolationWorld {
	t.Helper()
	w := &isolationWorld{engineWorld: newEngineWorld(t), ownIdentity: map[string]string{}}
	ctx := context.Background()

	identity := func(subject string) string {
		id, err := state.CreateIdentity(ctx, w.api, system, state.NewIdentity{
			Provider: model.IdentityProviderClerk, Subject: subject, PrimaryEmail: subject + "@example.test", DisplayName: subject})
		if err != nil {
			t.Fatalf("CreateIdentity: %v", err)
		}
		return id
	}
	link := func(space, identityID, name string) {
		if _, err := state.CreateParticipant(ctx, w.api, system, space,
			state.NewParticipant{Kind: model.ParticipantKindPerson, IdentityID: identityID, DisplayName: name}); err != nil {
			t.Fatalf("CreateParticipant: %v", err)
		}
	}
	w.sharedIdentity = identity("iso-shared")

	for _, space := range []string{w.spaceA, w.spaceB} {
		f := w.ledger(space)
		actor := w.writer(space, "writer")
		op := newOperation(t, f, "k-iso-"+space)
		if err := w.run(actor, space, "request-iso", op.standard()); err != nil {
			t.Fatalf("the operation failed in %s: %v", space, err)
		}
		var entryID string
		if err := w.admin.QueryRow(`SELECT id::text FROM kernel.entries WHERE exchange_id = $1 AND amount > 0`, op.exchange).Scan(&entryID); err != nil {
			t.Fatalf("read the entry: %v", err)
		}
		bookkeeper := model.Actor{Kind: model.ActorSystem, ParticipantID: f.creditor}
		if _, _, err := state.FulfillCommitment(ctx, w.api, bookkeeper, space, op.commitment, entryID); err != nil {
			t.Fatalf("FulfillCommitment: %v", err)
		}
		if err := state.EnableCapability(ctx, w.api, system, space,
			model.NewSpaceCapability{CapabilityKey: "test.billing", Source: model.CapabilitySourcePackage}); err != nil {
			t.Fatalf("EnableCapability: %v", err)
		}
		if _, err := state.RecordAccessAudit(ctx, w.api, system, space, model.NewAccessAudit{
			ActorParticipantID: actor.ParticipantID, ActorSpaceID: space, Action: model.AccessActionRead,
			Reason: "isolation test", RequestID: "request-audit"}); err != nil {
			t.Fatalf("RecordAccessAudit: %v", err)
		}
		w.ownIdentity[space] = identity("iso-own-" + space[len(space)-6:])
		link(space, w.ownIdentity[space], "Holder of an own identity")
		link(space, w.sharedIdentity, "Holder of the shared identity")
	}
	return w
}

// synthesizedInsert builds an insert of one row of table in space from the
// catalog alone: every column that is not null and has no default gets a value
// of its type, and the column space_id gets space. It is how a table the tests
// do not know yet is filled, and how the tests try to write a row into
// another space.
func synthesizedInsert(t *testing.T, admin *sql.DB, table, space string) (string, []any, error) {
	t.Helper()
	rows, err := admin.Query(`SELECT a.attname, format_type(a.atttypid, a.atttypmod)
		FROM pg_attribute a
		WHERE a.attrelid = ('kernel.' || quote_ident($1))::regclass AND a.attnum > 0 AND NOT a.attisdropped
		  AND a.attnotnull AND NOT a.atthasdef AND a.attgenerated = '' AND a.attidentity = ''
		ORDER BY a.attnum`, table)
	if err != nil {
		return "", nil, fmt.Errorf("read the columns of %s: %w", table, err)
	}
	defer rows.Close()
	var names, marks []string
	var args []any
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return "", nil, fmt.Errorf("scan a column of %s: %w", table, err)
		}
		value, err := synthesizedValue(t, name, typ, space)
		if err != nil {
			return "", nil, fmt.Errorf("column %s of %s: %w", name, table, err)
		}
		args = append(args, value)
		names = append(names, pgx.Identifier{name}.Sanitize())
		marks = append(marks, fmt.Sprintf("$%d::%s", len(args), typ))
	}
	if err := rows.Err(); err != nil {
		return "", nil, fmt.Errorf("read the columns of %s: %w", table, err)
	}
	if len(names) == 0 {
		return "", nil, fmt.Errorf("%s has no column to fill", table)
	}
	return "INSERT INTO kernel." + pgx.Identifier{table}.Sanitize() + " (" + strings.Join(names, ", ") + ") VALUES (" + strings.Join(marks, ", ") + ")", args, nil
}

// synthesizedValue returns a value for a column of the given type.
func synthesizedValue(t *testing.T, name, typ, space string) (any, error) {
	t.Helper()
	switch {
	case typ == "uuid" && name == "space_id":
		return space, nil
	case typ == "uuid":
		return newID(t), nil
	case typ == "text" || strings.HasPrefix(typ, "character varying"):
		return "isolation", nil
	case typ == "integer" || typ == "bigint" || typ == "smallint":
		return 1, nil
	case typ == "numeric" || strings.HasPrefix(typ, "numeric("):
		return "1", nil
	case typ == "boolean":
		return false, nil
	case typ == "timestamp with time zone":
		return time.Now().UTC(), nil
	case typ == "jsonb" || typ == "json":
		return "{}", nil
	case strings.HasSuffix(typ, "[]"):
		return "{}", nil
	case typ == "bytea":
		return []byte{0}, nil
	}
	return nil, fmt.Errorf("no value for type %s: extend synthesizedValue or seed the table by hand", typ)
}

// fillMissingTables gives one row in each space to every table with a space_id
// column that the seed left empty, which is how a table added by a later story
// joins the isolation test without editing it. The rows are written as the
// administrator with the triggers switched off, because only the isolation of
// reads and writes is under test here.
func fillMissingTables(t *testing.T, w *isolationWorld, tables []catalogTable) {
	t.Helper()
	for _, table := range tables {
		if !table.HasSpaceID {
			continue
		}
		for _, space := range []string{w.spaceA, w.spaceB} {
			var n int
			query := "SELECT count(*) FROM kernel." + pgx.Identifier{table.Name}.Sanitize() + " WHERE space_id = $1"
			if err := w.admin.QueryRow(query, space).Scan(&n); err != nil {
				t.Fatalf("count the rows of %s: %v", table.Name, err)
			}
			if n > 0 {
				continue
			}
			insert, args, err := synthesizedInsert(t, w.admin, table.Name, space)
			if err != nil {
				t.Errorf("%s has no rows in space %s and cannot be filled by itself: %v", table.Name, space, err)
				continue
			}
			tx, err := w.admin.Begin()
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			if _, err := tx.Exec(`SET LOCAL session_replication_role = replica`); err != nil {
				tx.Rollback()
				t.Fatalf("switch the triggers off: %v", err)
			}
			if _, err := tx.Exec(insert, args...); err != nil {
				tx.Rollback()
				t.Errorf("%s has no rows in space %s and its synthesized row failed: %v", table.Name, space, err)
				continue
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit the synthesized row of %s: %v", table.Name, err)
			}
		}
	}
}
