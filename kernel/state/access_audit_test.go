package state_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// supportAudit builds a valid audit entry of a platform actor.
func supportAudit(staff string) model.NewAccessAudit {
	return model.NewAccessAudit{
		ActorParticipantID: staff, ActorSpaceID: spacePlatform, Action: model.AccessActionRead,
		SubjectRef: "participant:x", Reason: "support ticket 42", RequestID: "req-1",
	}
}

// TestRecordAccessAuditStoresAndLists: an entry is stored in the audited space
// and listed in order.
func TestRecordAccessAuditStoresAndLists(t *testing.T) {
	t.Parallel()
	_, api, staff := grantSetup(t)
	ctx := context.Background()

	first, err := state.RecordAccessAudit(ctx, api, system, spaceA, supportAudit(staff))
	if err != nil {
		t.Fatalf("RecordAccessAudit: %v", err)
	}
	second := supportAudit(staff)
	second.Action, second.RequestID, second.SubjectRef = model.AccessActionWrite, "req-2", ""
	secondID, err := state.RecordAccessAudit(ctx, api, system, spaceA, second)
	if err != nil {
		t.Fatalf("RecordAccessAudit: %v", err)
	}

	got, err := state.ListAccessAudit(ctx, api, spaceA, 10)
	if err != nil {
		t.Fatalf("ListAccessAudit: %v", err)
	}
	if len(got) != 2 || got[0].ID != first || got[1].ID != secondID {
		t.Fatalf("entries = %+v, want the two in order", got)
	}
	want := model.AccessAudit{
		ID: first, SpaceID: spaceA, ActorParticipantID: staff, ActorSpaceID: spacePlatform,
		Action: model.AccessActionRead, SubjectRef: "participant:x", Reason: "support ticket 42", RequestID: "req-1",
	}
	got[0].OccurredAt = time.Time{}
	if got[0] != want {
		t.Errorf("entry = %+v, want %+v", got[0], want)
	}
	if limited, err := state.ListAccessAudit(ctx, api, spaceA, 1); err != nil || len(limited) != 1 {
		t.Errorf("limit 1: %d entries, %v", len(limited), err)
	}
	for _, limit := range []int{0, -1, 1001} {
		_, err := state.ListAccessAudit(ctx, api, spaceA, limit)
		requireCode(t, err, model.CodeInvalidInput)
	}
}

// TestRecordAccessAuditWithGrant links an entry to the grant that allowed it.
func TestRecordAccessAuditWithGrant(t *testing.T) {
	t.Parallel()
	admin, api, staff := grantSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	role := mustRole(t, api, spaceA, ownerRole())
	expiry := time.Now().Add(time.Hour)
	grantID := mustGrant(t, api, model.SystemClock{}, spaceA, model.NewGrant{
		ParticipantID: holder, RoleID: role, Reason: "ticket", ExpiresAt: &expiry,
		GrantedBy: &model.Grantor{ParticipantID: staff, SpaceID: spacePlatform},
	})
	in := supportAudit(staff)
	in.GrantID = grantID
	id, err := state.RecordAccessAudit(context.Background(), api, system, spaceA, in)
	if err != nil {
		t.Fatalf("RecordAccessAudit: %v", err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.access_audit WHERE id = $1 AND grant_id = $2`, id, grantID); got != 1 {
		t.Errorf("linked rows = %d, want 1", got)
	}

	other := supportAudit(staff)
	other.GrantID = "01900000-0000-7000-8000-000000000f98"
	_, err = state.RecordAccessAudit(context.Background(), api, system, spaceA, other)
	requireCode(t, err, model.CodeRuleViolation)
}

// TestRecordAccessAuditRequiresReasonAndRequestID guards criterion 6: an
// entry without a reason or a request id is not written.
func TestRecordAccessAuditRequiresReasonAndRequestID(t *testing.T) {
	t.Parallel()
	admin, api, staff := grantSetup(t)
	cases := map[string]func(*model.NewAccessAudit){
		"empty reason":     func(a *model.NewAccessAudit) { a.Reason = "" },
		"blank reason":     func(a *model.NewAccessAudit) { a.Reason = "  " },
		"empty request id": func(a *model.NewAccessAudit) { a.RequestID = "" },
		"blank request id": func(a *model.NewAccessAudit) { a.RequestID = " " },
		"unknown action":   func(a *model.NewAccessAudit) { a.Action = "delete" },
		"blank subject":    func(a *model.NewAccessAudit) { a.SubjectRef = "  " },
		"bad actor":        func(a *model.NewAccessAudit) { a.ActorParticipantID = "x" },
		"bad actor space":  func(a *model.NewAccessAudit) { a.ActorSpaceID = "" },
		"bad grant id":     func(a *model.NewAccessAudit) { a.GrantID = "x" },
	}
	for name, mutate := range cases {
		in := supportAudit(staff)
		mutate(&in)
		_, err := state.RecordAccessAudit(context.Background(), api, system, spaceA, in)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("%s: error = %v, want invalid_input", name, err)
		}
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.access_audit`); got != 0 {
		t.Errorf("entries = %d, want 0", got)
	}
}

// TestRolesGrantsCapabilitiesAuditIsolationBetweenSpaces guards criterion 7:
// from space B nothing of A is visible, and writing a row of A is refused.
func TestRolesGrantsCapabilitiesAuditIsolationBetweenSpaces(t *testing.T) {
	t.Parallel()
	admin, api, staff := grantSetup(t)
	ctx := context.Background()
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	role := mustRole(t, api, spaceA, ownerRole())
	grantID := mustGrant(t, api, model.SystemClock{}, spaceA, model.NewGrant{ParticipantID: holder, RoleID: role})
	if err := state.EnableCapability(ctx, api, system, spaceA, model.NewSpaceCapability{CapabilityKey: "test.capability", Source: model.CapabilitySourceCode}); err != nil {
		t.Fatalf("EnableCapability: %v", err)
	}
	if _, err := state.RecordAccessAudit(ctx, api, system, spaceA, supportAudit(staff)); err != nil {
		t.Fatalf("RecordAccessAudit: %v", err)
	}

	tables := []string{"roles", "grants", "space_capabilities", "access_audit"}
	for _, table := range tables {
		if got := count(t, admin, `SELECT count(*) FROM kernel.`+table+` WHERE space_id = $1`, spaceA); got != 1 {
			t.Fatalf("%s rows of A = %d, want 1", table, got)
		}
	}
	visible(t, api, spaceB, func(tx *sql.Tx) {
		for _, table := range tables {
			if got := count(t, tx, `SELECT count(*) FROM kernel.`+table); got != 0 {
				t.Errorf("space B sees %d rows of %s", got, table)
			}
		}
	})

	inserts := map[string]string{
		"roles":              `INSERT INTO kernel.roles (id, space_id, key, name, permissions, system) VALUES ('01900000-0000-7000-8000-000000000581', $1, 'x', 'X', '{}', false)`,
		"grants":             `INSERT INTO kernel.grants (id, space_id, participant_id, role_id) VALUES ('01900000-0000-7000-8000-000000000582', $1, $2, $3)`,
		"space_capabilities": `INSERT INTO kernel.space_capabilities (space_id, capability_key, source) VALUES ($1, 'test.other', 'code')`,
		"access_audit": `INSERT INTO kernel.access_audit (id, space_id, actor_participant_id, actor_space_id, action, reason, request_id)
			VALUES ('01900000-0000-7000-8000-000000000583', $1, '` + staff + `', '` + spacePlatform + `', 'read', 'why', 'req')`,
	}
	for table, query := range inserts {
		err := state.InSpace(ctx, api, spaceB, func(ctx context.Context, tx *sql.Tx) error {
			args := []any{spaceA}
			if table == "grants" {
				args = append(args, holder, role)
			}
			_, err := tx.ExecContext(ctx, query, args...)
			return err
		})
		requireSQLState(t, err, "42501")
	}

	// A revocation from space B cannot touch the grant of A.
	requireCode(t, state.RevokeGrant(ctx, api, system, spaceB, grantID), model.CodeNotFound)
	if got := count(t, admin, `SELECT count(*) FROM kernel.grants WHERE revoked_at IS NULL`); got != 1 {
		t.Errorf("active grants = %d, want 1", got)
	}
}

// TestRecordAccessAuditActorScope: an actor of another business space cannot
// audit space A, while a platform actor and an actor of A itself can.
func TestRecordAccessAuditActorScope(t *testing.T) {
	t.Parallel()
	admin, api, staff := grantSetup(t)
	ctx := context.Background()
	outsider := mustParticipant(t, api, spaceB, newPerson("", "Outsider"))
	insider := mustParticipant(t, api, spaceA, newPerson("", "Insider"))

	in := supportAudit(outsider)
	in.ActorSpaceID = spaceB
	_, err := state.RecordAccessAudit(ctx, api, system, spaceA, in)
	if code, ok := model.CodeOf(err); !ok || (code != model.CodeInvalidInput && code != model.CodeRuleViolation) {
		t.Errorf("business actor of another space: error = %v", err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.access_audit`); got != 0 {
		t.Errorf("entries = %d, want 0", got)
	}
	if _, err := state.RecordAccessAudit(ctx, api, system, spaceA, supportAudit(staff)); err != nil {
		t.Errorf("platform actor: %v", err)
	}
	same := supportAudit(insider)
	same.ActorSpaceID = spaceA
	if _, err := state.RecordAccessAudit(ctx, api, system, spaceA, same); err != nil {
		t.Errorf("actor of the audited space: %v", err)
	}
}
