package capability_test

import (
	"context"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

// TestSystemHasTimerProcessAndOwnerDoesNot guards the permission that execution-13
// will check before firing timers: the system actor holds it, and the owner of a
// business, a person with the standard role, does not.
func TestSystemHasTimerProcessAndOwnerDoesNot(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if err := state.CreateStandardRoles(context.Background(), w.api, system, w.spaceA); err != nil {
		t.Fatalf("CreateStandardRoles: %v", err)
	}
	var ownerRole string
	if err := w.admin.QueryRow(`SELECT id::text FROM kernel.roles WHERE space_id = $1 AND key = 'owner'`, w.spaceA).Scan(&ownerRole); err != nil {
		t.Fatalf("read owner role: %v", err)
	}
	owner := w.person(w.spaceA, "owner")
	w.grant(w.spaceA, owner, ownerRole)

	requireAllowed(t, "system kernel.timer.process", w.check(w.spaceA, system, req(model.PermissionTimerProcess)))
	requireCode(t, "owner kernel.timer.process", w.check(w.spaceA, owner, req(model.PermissionTimerProcess)), model.CodePermissionDenied)
	for _, kind := range []model.ActorKind{model.ActorPerson, model.ActorAgent, model.ActorAPIKey} {
		actor := model.Actor{Kind: kind, ParticipantID: owner.ParticipantID}
		requireCode(t, string(kind)+" kernel.timer.process", w.check(w.spaceA, actor, req(model.PermissionTimerProcess)), model.CodePermissionDenied)
	}
	requireCode(t, "null actor kernel.timer.process", w.check(w.spaceA, model.Actor{}, req(model.PermissionTimerProcess)), model.CodePermissionDenied)
}
