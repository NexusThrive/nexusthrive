package state_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// mustGrant grants a role and returns the grant id.
func mustGrant(t *testing.T, api *sql.DB, clock model.Clock, spaceID string, in model.NewGrant) string {
	t.Helper()
	id, err := state.GrantRole(context.Background(), api, clock, system, spaceID, in)
	if err != nil {
		t.Fatalf("GrantRole: %v", err)
	}
	return id
}

// TestGrantRoleStoresWithNullRevokedAt guards criterion 2.
func TestGrantRoleStoresWithNullRevokedAt(t *testing.T) {
	t.Parallel()
	admin, api, _ := grantSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	grantor := mustParticipant(t, api, spaceA, newPerson("", "Grantor"))
	role := mustRole(t, api, spaceA, ownerRole())

	id := mustGrant(t, api, model.SystemClock{}, spaceA, model.NewGrant{
		ParticipantID: holder, RoleID: role, ExtraPermissions: []string{"kernel.test.write"},
		GrantedBy: &model.Grantor{ParticipantID: grantor, SpaceID: spaceA}, Reason: "onboarding",
	})
	got, err := state.GetGrant(context.Background(), api, spaceA, id)
	if err != nil {
		t.Fatalf("GetGrant: %v", err)
	}
	if got.ID != id || got.SpaceID != spaceA || got.ParticipantID != holder || got.RoleID != role {
		t.Errorf("grant = %+v", got)
	}
	if got.RevokedAt != nil || got.ExpiresAt != nil {
		t.Errorf("revoked_at = %v, expires_at = %v, want both nil", got.RevokedAt, got.ExpiresAt)
	}
	if got.GrantedBy == nil || *got.GrantedBy != (model.Grantor{ParticipantID: grantor, SpaceID: spaceA}) {
		t.Errorf("granted_by = %+v", got.GrantedBy)
	}
	if got.Reason != "onboarding" || len(got.ExtraPermissions) != 1 || got.ExtraPermissions[0] != "kernel.test.write" {
		t.Errorf("reason = %q, extra = %v", got.Reason, got.ExtraPermissions)
	}
	if time.Since(got.CreatedAt) > time.Minute || got.CreatedAt.Location() != time.UTC {
		t.Errorf("created_at = %v", got.CreatedAt)
	}
	if n := count(t, admin, `SELECT count(*) FROM kernel.grants WHERE id = $1 AND revoked_at IS NULL`, id); n != 1 {
		t.Errorf("stored active rows = %d, want 1", n)
	}
}

// TestGrantRoleWithoutGrantor stores a grant with no grantor columns.
func TestGrantRoleWithoutGrantor(t *testing.T) {
	t.Parallel()
	_, api, _ := grantSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	role := mustRole(t, api, spaceA, ownerRole())
	id := mustGrant(t, api, model.SystemClock{}, spaceA, model.NewGrant{ParticipantID: holder, RoleID: role})
	got, err := state.GetGrant(context.Background(), api, spaceA, id)
	if err != nil {
		t.Fatalf("GetGrant: %v", err)
	}
	if got.GrantedBy != nil || got.Reason != "" || len(got.ExtraPermissions) != 0 {
		t.Errorf("grant = %+v", got)
	}
}

// TestGrantRoleRejectsForeignParticipantOrRole guards criterion 2: nothing is
// written when the participant or the role belongs to another space.
func TestGrantRoleRejectsForeignParticipantOrRole(t *testing.T) {
	t.Parallel()
	admin, api, _ := grantSetup(t)
	holderA := mustParticipant(t, api, spaceA, newPerson("", "Holder A"))
	holderB := mustParticipant(t, api, spaceB, newPerson("", "Holder B"))
	roleA := mustRole(t, api, spaceA, ownerRole())
	roleB := mustRole(t, api, spaceB, ownerRole())

	for name, in := range map[string]model.NewGrant{
		"participant of another space": {ParticipantID: holderB, RoleID: roleA},
		"role of another space":        {ParticipantID: holderA, RoleID: roleB},
		"unknown participant":          {ParticipantID: "01900000-0000-7000-8000-000000000f98", RoleID: roleA},
		"unknown role":                 {ParticipantID: holderA, RoleID: "01900000-0000-7000-8000-000000000f98"},
	} {
		_, err := state.GrantRole(context.Background(), api, model.SystemClock{}, system, spaceA, in)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeRuleViolation {
			t.Errorf("%s: error = %v, want rule_violation", name, err)
		}
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.grants`); got != 0 {
		t.Errorf("grants = %d, want 0", got)
	}
}

// TestGrantRoleRejectsGrantorOfAnotherBusinessSpace: only the same space or a
// platform space may grant (ADR-0029).
func TestGrantRoleRejectsGrantorOfAnotherBusinessSpace(t *testing.T) {
	t.Parallel()
	admin, api, _ := grantSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	grantorB := mustParticipant(t, api, spaceB, newPerson("", "Grantor B"))
	role := mustRole(t, api, spaceA, ownerRole())

	_, err := state.GrantRole(context.Background(), api, model.SystemClock{}, system, spaceA, model.NewGrant{
		ParticipantID: holder, RoleID: role, GrantedBy: &model.Grantor{ParticipantID: grantorB, SpaceID: spaceB},
		Reason: "ticket", ExpiresAt: ptr(time.Now().Add(time.Hour)),
	})
	if code, ok := model.CodeOf(err); !ok || (code != model.CodeInvalidInput && code != model.CodeRuleViolation) {
		t.Errorf("error = %v, want invalid_input or rule_violation", err)
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.grants`); got != 0 {
		t.Errorf("grants = %d, want 0", got)
	}
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// TestGrantRoleDuplicateActiveIsRuleViolation: one active grant per
// participant and role; after a revocation the role can be granted again.
func TestGrantRoleDuplicateActiveIsRuleViolation(t *testing.T) {
	t.Parallel()
	_, api, _ := grantSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	role := mustRole(t, api, spaceA, ownerRole())
	in := model.NewGrant{ParticipantID: holder, RoleID: role}

	first := mustGrant(t, api, model.SystemClock{}, spaceA, in)
	_, err := state.GrantRole(context.Background(), api, model.SystemClock{}, system, spaceA, in)
	requireCode(t, err, model.CodeRuleViolation)
	if err := state.RevokeGrant(context.Background(), api, system, spaceA, first); err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}
	mustGrant(t, api, model.SystemClock{}, spaceA, in)
}

// TestGrantRoleRejectsMalformedInput covers the checks done before the database.
func TestGrantRoleRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	admin, api, _ := grantSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	role := mustRole(t, api, spaceA, ownerRole())

	cases := map[string]func(*model.NewGrant){
		"bad participant id":   func(g *model.NewGrant) { g.ParticipantID = "nope" },
		"empty role id":        func(g *model.NewGrant) { g.RoleID = "" },
		"bad extra permission": func(g *model.NewGrant) { g.ExtraPermissions = []string{"Bad"} },
		"blank reason":         func(g *model.NewGrant) { g.Reason = " " },
		"bad grantor id":       func(g *model.NewGrant) { g.GrantedBy = &model.Grantor{ParticipantID: "x", SpaceID: spaceA} },
		"bad grantor space":    func(g *model.NewGrant) { g.GrantedBy = &model.Grantor{ParticipantID: holder, SpaceID: "x"} },
		"self grant":           func(g *model.NewGrant) { g.GrantedBy = &model.Grantor{ParticipantID: holder, SpaceID: spaceA} },
	}
	for name, mutate := range cases {
		in := model.NewGrant{ParticipantID: holder, RoleID: role, Reason: "ok"}
		mutate(&in)
		// A blank reason is the one case that starts from a valid reason.
		if name == "blank reason" {
			in.Reason = " "
		}
		_, err := state.GrantRole(context.Background(), api, model.SystemClock{}, system, spaceA, in)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("%s: error = %v, want invalid_input", name, err)
		}
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.grants`); got != 0 {
		t.Errorf("grants = %d, want 0", got)
	}
}

// TestGrantRoleRejectsPastExpiry: the kernel never creates a grant that is
// already expired, measured with the injected clock.
func TestGrantRoleRejectsPastExpiry(t *testing.T) {
	t.Parallel()
	admin, api, _ := grantSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	role := mustRole(t, api, spaceA, ownerRole())
	now := time.Now().UTC()
	clock := &fixedClock{at: now}

	for name, expiry := range map[string]time.Time{"past": now.Add(-time.Second), "equal to now": now} {
		_, err := state.GrantRole(context.Background(), api, clock, system, spaceA, model.NewGrant{
			ParticipantID: holder, RoleID: role, ExpiresAt: &expiry,
		})
		if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("%s: error = %v, want invalid_input", name, err)
		}
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.grants`); got != 0 {
		t.Errorf("grants = %d, want 0", got)
	}
}

// TestIsGrantActiveUsesInjectedClock guards criterion 3: the same stored grant
// is active or not depending only on the clock the caller injects.
func TestIsGrantActiveUsesInjectedClock(t *testing.T) {
	t.Parallel()
	_, api, _ := grantSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	role := mustRole(t, api, spaceA, ownerRole())
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := &fixedClock{at: now}

	id := mustGrant(t, api, clock, spaceA, model.NewGrant{ParticipantID: holder, RoleID: role, ExpiresAt: ptr(now.Add(time.Hour))})
	check := func(label string, at time.Time, want bool) {
		t.Helper()
		clock.at = at
		got, err := state.IsGrantActive(context.Background(), api, clock, spaceA, id)
		if err != nil {
			t.Fatalf("%s: IsGrantActive: %v", label, err)
		}
		if got != want {
			t.Errorf("%s: active = %v, want %v", label, got, want)
		}
	}
	check("before expiry", now, true)
	check("just before expiry", now.Add(time.Hour-time.Microsecond), true)
	check("at expiry", now.Add(time.Hour), false)
	check("after expiry", now.Add(2*time.Hour), false)
	check("back before expiry", now, true)

	if err := state.RevokeGrant(context.Background(), api, system, spaceA, id); err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}
	check("revoked, before expiry", now, false)
}

// TestIsGrantActiveForRowsWrittenByAdministrator guards criterion 3 as the
// criterion words it: an already expired grant and a revoked grant, which the
// kernel refuses to create, are not active.
func TestIsGrantActiveForRowsWrittenByAdministrator(t *testing.T) {
	t.Parallel()
	admin, api, _ := grantSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	other := mustParticipant(t, api, spaceA, newPerson("", "Other"))
	role := mustRole(t, api, spaceA, ownerRole())
	const expired = "01900000-0000-7000-8000-000000000571"
	const revoked = "01900000-0000-7000-8000-000000000572"

	if _, err := admin.Exec(`INSERT INTO kernel.grants (id, space_id, participant_id, role_id, expires_at)
		VALUES ($1, $2, $3, $4, now() - interval '1 hour')`, expired, spaceA, holder, role); err != nil {
		t.Fatalf("insert expired: %v", err)
	}
	if _, err := admin.Exec(`INSERT INTO kernel.grants (id, space_id, participant_id, role_id, expires_at, revoked_at)
		VALUES ($1, $2, $3, $4, now() + interval '1 hour', now())`, revoked, spaceA, other, role); err != nil {
		t.Fatalf("insert revoked: %v", err)
	}
	for name, id := range map[string]string{"expired": expired, "revoked": revoked} {
		got, err := state.IsGrantActive(context.Background(), api, model.SystemClock{}, spaceA, id)
		if err != nil || got {
			t.Errorf("%s: active = %v, err = %v, want false", name, got, err)
		}
	}
}

// TestIsGrantActiveUnknownIsNotFound: an id the space cannot see.
func TestIsGrantActiveUnknownIsNotFound(t *testing.T) {
	t.Parallel()
	_, api, _ := grantSetup(t)
	holder := mustParticipant(t, api, spaceB, newPerson("", "Holder B"))
	role := mustRole(t, api, spaceB, ownerRole())
	id := mustGrant(t, api, model.SystemClock{}, spaceB, model.NewGrant{ParticipantID: holder, RoleID: role})

	for name, grantID := range map[string]string{"unknown": "01900000-0000-7000-8000-000000000f98", "other space": id} {
		_, err := state.IsGrantActive(context.Background(), api, model.SystemClock{}, spaceA, grantID)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeNotFound {
			t.Errorf("%s: error = %v, want not_found", name, err)
		}
	}
	_, err := state.IsGrantActive(context.Background(), api, model.SystemClock{}, spaceA, "nope")
	requireCode(t, err, model.CodeInvalidInput)
	_, err = state.GetGrant(context.Background(), api, spaceA, id)
	requireCode(t, err, model.CodeNotFound)
}

// TestSupportGrantRequiresReasonAndExpiry guards criterion 4: a grantor of a
// platform space acting in another space needs a reason and an expiry.
func TestSupportGrantRequiresReasonAndExpiry(t *testing.T) {
	t.Parallel()
	admin, api, staff := grantSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	role := mustRole(t, api, spaceA, ownerRole())
	grantor := &model.Grantor{ParticipantID: staff, SpaceID: spacePlatform}
	expiry := time.Now().Add(time.Hour)

	for name, in := range map[string]model.NewGrant{
		"no reason":    {ParticipantID: holder, RoleID: role, GrantedBy: grantor, ExpiresAt: &expiry},
		"blank reason": {ParticipantID: holder, RoleID: role, GrantedBy: grantor, ExpiresAt: &expiry, Reason: "   "},
		"no expiry":    {ParticipantID: holder, RoleID: role, GrantedBy: grantor, Reason: "ticket 42"},
	} {
		_, err := state.GrantRole(context.Background(), api, model.SystemClock{}, system, spaceA, in)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeInvalidInput {
			t.Errorf("%s: error = %v, want invalid_input", name, err)
		}
	}
	if got := count(t, admin, `SELECT count(*) FROM kernel.grants`); got != 0 {
		t.Errorf("grants = %d, want 0", got)
	}

	id := mustGrant(t, api, model.SystemClock{}, spaceA, model.NewGrant{
		ParticipantID: holder, RoleID: role, GrantedBy: grantor, ExpiresAt: &expiry, Reason: "ticket 42",
	})
	got, err := state.GetGrant(context.Background(), api, spaceA, id)
	if err != nil {
		t.Fatalf("GetGrant: %v", err)
	}
	if got.GrantedBy == nil || got.GrantedBy.SpaceID != spacePlatform || got.ExpiresAt == nil || got.Reason != "ticket 42" {
		t.Errorf("support grant = %+v", got)
	}
	var kind string
	if err := admin.QueryRow(`SELECT granted_by_space_kind FROM kernel.grants WHERE id = $1`, id).Scan(&kind); err != nil || kind != "platform" {
		t.Errorf("stored grantor kind = %q (%v), want platform", kind, err)
	}
}

// TestPlatformStaffGrantInsideOwnSpaceNeedsNoExpiry: adding staff in the
// platform space is not support access.
func TestPlatformStaffGrantInsideOwnSpaceNeedsNoExpiry(t *testing.T) {
	t.Parallel()
	_, api, staff := grantSetup(t)
	colleague := mustParticipant(t, api, spacePlatform, newPerson("", "Colleague"))
	role := mustRole(t, api, spacePlatform, model.NewRole{Key: "staff", Name: "Staff", Permissions: []string{"kernel.test.read"}, System: true})

	mustGrant(t, api, model.SystemClock{}, spacePlatform, model.NewGrant{
		ParticipantID: colleague, RoleID: role, GrantedBy: &model.Grantor{ParticipantID: staff, SpaceID: spacePlatform},
	})
}

// TestRevokeGrantTwiceReturnsAlreadyDoneKeepsRevokedAt guards criterion 8.
func TestRevokeGrantTwiceReturnsAlreadyDoneKeepsRevokedAt(t *testing.T) {
	t.Parallel()
	admin, api, _ := grantSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	role := mustRole(t, api, spaceA, ownerRole())
	id := mustGrant(t, api, model.SystemClock{}, spaceA, model.NewGrant{ParticipantID: holder, RoleID: role})

	before := time.Now().Add(-time.Minute)
	if err := state.RevokeGrant(context.Background(), api, system, spaceA, id); err != nil {
		t.Fatalf("first RevokeGrant: %v", err)
	}
	var first time.Time
	if err := admin.QueryRow(`SELECT revoked_at FROM kernel.grants WHERE id = $1`, id).Scan(&first); err != nil {
		t.Fatalf("read revoked_at: %v", err)
	}
	if first.Before(before) {
		t.Errorf("revoked_at = %v, want a recent database time", first)
	}

	err := state.RevokeGrant(context.Background(), api, system, spaceA, id)
	requireCode(t, err, model.CodeAlreadyDone)
	var second time.Time
	if err := admin.QueryRow(`SELECT revoked_at FROM kernel.grants WHERE id = $1`, id).Scan(&second); err != nil {
		t.Fatalf("read revoked_at: %v", err)
	}
	if !second.Equal(first) {
		t.Errorf("revoked_at changed from %v to %v", first, second)
	}
}

// TestRevokeGrantConcurrentExactlyOneSucceeds guards criterion 8 under
// concurrency: one caller revokes, the others get already_done.
func TestRevokeGrantConcurrentExactlyOneSucceeds(t *testing.T) {
	t.Parallel()
	_, api, _ := grantSetup(t)
	holder := mustParticipant(t, api, spaceA, newPerson("", "Holder"))
	role := mustRole(t, api, spaceA, ownerRole())
	id := mustGrant(t, api, model.SystemClock{}, spaceA, model.NewGrant{ParticipantID: holder, RoleID: role})

	const callers = 8
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- state.RevokeGrant(context.Background(), api, system, spaceA, id)
		}()
	}
	wg.Wait()
	close(results)
	succeeded, already := 0, 0
	for err := range results {
		switch code, _ := model.CodeOf(err); {
		case err == nil:
			succeeded++
		case code == model.CodeAlreadyDone:
			already++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || already != callers-1 {
		t.Errorf("succeeded = %d, already_done = %d, want 1 and %d", succeeded, already, callers-1)
	}
}

// TestRevokeGrantUnknownIsNotFound: an id that does not exist, or belongs to
// another space, is not_found and a malformed one is invalid_input.
func TestRevokeGrantUnknownIsNotFound(t *testing.T) {
	t.Parallel()
	_, api, _ := grantSetup(t)
	holder := mustParticipant(t, api, spaceB, newPerson("", "Holder B"))
	role := mustRole(t, api, spaceB, ownerRole())
	idB := mustGrant(t, api, model.SystemClock{}, spaceB, model.NewGrant{ParticipantID: holder, RoleID: role})

	for name, id := range map[string]string{"unknown": "01900000-0000-7000-8000-000000000f98", "other space": idB} {
		err := state.RevokeGrant(context.Background(), api, system, spaceA, id)
		if code, ok := model.CodeOf(err); !ok || code != model.CodeNotFound {
			t.Errorf("%s: error = %v, want not_found", name, err)
		}
	}
	requireCode(t, state.RevokeGrant(context.Background(), api, system, spaceA, "nope"), model.CodeInvalidInput)
	// The grant of B is untouched.
	got, err := state.GetGrant(context.Background(), api, spaceB, idB)
	if err != nil || got.RevokedAt != nil {
		t.Errorf("grant of B = %+v, %v, want active", got, err)
	}
}

// TestGrantOperationsRequireSystemActor: until permissions arrive, only the
// system actor writes any of these tables.
func TestGrantOperationsRequireSystemActor(t *testing.T) {
	t.Parallel()
	admin, api, staff := grantSetup(t)
	person := model.Actor{Kind: model.ActorPerson, ParticipantID: staff}
	ctx := context.Background()

	_, err := state.CreateRole(ctx, api, person, spaceA, ownerRole())
	requireCode(t, err, model.CodePermissionDenied)
	_, err = state.GrantRole(ctx, api, model.SystemClock{}, person, spaceA, model.NewGrant{})
	requireCode(t, err, model.CodePermissionDenied)
	requireCode(t, state.RevokeGrant(ctx, api, person, spaceA, "x"), model.CodePermissionDenied)
	requireCode(t, state.EnableCapability(ctx, api, person, spaceA, model.NewSpaceCapability{}), model.CodePermissionDenied)
	_, err = state.RecordAccessAudit(ctx, api, person, spaceA, model.NewAccessAudit{})
	requireCode(t, err, model.CodePermissionDenied)

	for _, table := range []string{"roles", "grants", "space_capabilities", "access_audit"} {
		if got := count(t, admin, `SELECT count(*) FROM kernel.`+table); got != 0 {
			t.Errorf("%s rows = %d, want 0", table, got)
		}
	}
}
