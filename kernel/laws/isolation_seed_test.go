package laws_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nexusthrive/nexusthrive/kernel/db"
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
// of its type, and the column space_id gets space. It is a last resort for a
// table the tests do not know yet: the values only respect the types, so a
// table with CHECK constraints needs an entry in seedOverrides instead.
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

// seedOverrides gives, by table name, a function that writes one valid row of
// the table in a space through its real path, with the triggers and CHECK
// constraints on. A table whose constraints the synthesized row cannot meet
// needs an entry here; fillMissingTables says so when it does not have one.
var seedOverrides = map[string]func(t *testing.T, w *isolationWorld, space string) error{
	// A record has a dotted operation, a 32-byte fingerprint and an actor of the
	// space; the generation and the expiry are stamped by the trigger.
	"idempotency_records": func(t *testing.T, w *isolationWorld, space string) error {
		var actor string
		if err := w.admin.QueryRow(`SELECT id::text FROM kernel.participants WHERE space_id = $1 LIMIT 1`, space).Scan(&actor); err != nil {
			return err
		}
		return runIn(t, w.api, space, stmt{
			`INSERT INTO kernel.idempotency_records (id, space_id, key, operation, actor_participant_id, request_hash, response, created_at)
			 VALUES ($1, $2, 'isolation-key', 'test.isolation', $3, decode(repeat('ab', 32), 'hex'), '{}'::jsonb, now())`,
			[]any{newID(t), space, actor},
		})
	},
}

// fillMissingTables gives one row in each space to every table with a space_id
// column that the seed left empty, which is how a table added by a later story
// joins the isolation test. A table with an entry in overrides is filled by
// it, through its real path; any other is filled from the catalog as the
// administrator with the triggers switched off, which only respects types. It
// returns what it could not fill, each with the override to add.
func fillMissingTables(t *testing.T, w *isolationWorld, tables []catalogTable, overrides map[string]func(*testing.T, *isolationWorld, string) error) []string {
	t.Helper()
	var failures []string
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
			if override, ok := overrides[table.Name]; ok {
				if err := override(t, w, space); err != nil {
					failures = append(failures, fmt.Sprintf("%s: its seedOverrides entry failed in space %s: %v", table.Name, space, err))
				}
				continue
			}
			insert, args, err := synthesizedInsert(t, w.admin, table.Name, space)
			if err == nil {
				err = insertWithoutTriggers(w.admin, insert, args)
			}
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: no rows in space %s and it cannot be filled from the catalog (%v); add an entry for it to seedOverrides that writes a valid row", table.Name, space, err))
			}
		}
	}
	return failures
}

// insertWithoutTriggers runs an insert as the administrator with the triggers
// and foreign keys off; CHECK constraints still apply.
func insertWithoutTriggers(admin *sql.DB, query string, args []any) error {
	tx, err := admin.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SET LOCAL session_replication_role = replica`); err != nil {
		return err
	}
	if _, err := tx.Exec(query, args...); err != nil {
		return err
	}
	return tx.Commit()
}

// canInsertAnyColumn reports whether the API role may insert into the table.
func canInsertAnyColumn(t *testing.T, admin *sql.DB, table string) bool {
	t.Helper()
	var ok bool
	if err := admin.QueryRow(`SELECT has_any_column_privilege($1, ('kernel.' || quote_ident($2))::regclass, 'INSERT')`, db.APIRole, table).Scan(&ok); err != nil {
		t.Fatalf("read the insert privilege of %s: %v", table, err)
	}
	return ok
}

// crossInsert tries to write into space other, as the API role acting in space
// acting, a copy of a real row of the table with only its space_id (and its
// id) changed, and returns the SQLSTATE of the attempt. Because the copy is a
// valid row, a refusal for any reason but the insert policy would be a
// different rule at work, so the statement runs with the triggers and foreign
// keys off, which leaves the policy as the first check that can refuse it. The
// control is the same row written in its own space, where the policy lets it
// pass: it fails, if at all, for another reason, which shows the policy was
// what stopped the attack. Both attempts are rolled back.
func crossInsert(t *testing.T, w *isolationWorld, table, acting, other string) (attack, control string, err error) {
	t.Helper()
	var document string
	err = w.admin.QueryRow(`SELECT to_jsonb(x)::text FROM kernel.`+pgx.Identifier{table}.Sanitize()+` x WHERE space_id = $1 LIMIT 1`, acting).Scan(&document)
	if err != nil {
		return "", "", fmt.Errorf("read a row to copy: %w", err)
	}
	rows, err := w.admin.Query(`SELECT a.attname FROM pg_attribute a
		WHERE a.attrelid = ('kernel.' || quote_ident($1))::regclass AND a.attnum > 0 AND NOT a.attisdropped AND a.attgenerated = ''
		  AND has_column_privilege($2, a.attrelid, a.attnum, 'INSERT') ORDER BY a.attnum`, table, db.APIRole)
	if err != nil {
		return "", "", fmt.Errorf("read the columns: %w", err)
	}
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			rows.Close()
			return "", "", err
		}
		columns = append(columns, pgx.Identifier{column}.Sanitize())
	}
	rows.Close()
	if len(columns) == 0 {
		return "42501", "42501", nil
	}
	list := strings.Join(columns, ", ")
	query := `INSERT INTO kernel.` + pgx.Identifier{table}.Sanitize() + ` (` + list + `)
		SELECT ` + list + ` FROM jsonb_populate_record(NULL::kernel.` + pgx.Identifier{table}.Sanitize() + `,
		$1::jsonb || jsonb_build_object('space_id', $2::text, 'id', $3::text))`

	attempt := func(space string) string {
		tx, err := w.admin.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback()
		for _, statement := range []string{`SET LOCAL session_replication_role = replica`, `SET LOCAL ROLE ` + pgx.Identifier{db.APIRole}.Sanitize()} {
			if _, err := tx.Exec(statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
		if _, err := tx.Exec(`SELECT set_config('kernel.space_id', $1, true)`, space); err != nil {
			t.Fatalf("set space: %v", err)
		}
		_, err = tx.Exec(query, document, other, newID(t))
		return pgCode(err)
	}
	return attempt(acting), attempt(other), nil
}
