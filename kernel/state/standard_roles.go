package state

import (
	"context"
	"database/sql"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// CreateStandardRoles creates the roles every space starts with, which today is
// the owner role of model.OwnerRole, in spaceID. Only the system actor may call
// it: a role marked as a system role is not made by a role of the space
// (ADR-0029, rule 3).
//
// The keys are checked against the kernel catalog before anything is written,
// and the role is written in one transaction through CreateRole, so the call
// is all or nothing. Calling it twice is rule_violation: the owner role already
// exists and nothing changes. The role never holds kernel.capability.write
// (rule 9) and no code treats it as an authority: it is only a list of keys.
func CreateStandardRoles(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string) error {
	if actor.Kind != model.ActorSystem {
		return model.NewError(model.CodePermissionDenied, "only the system actor creates the standard roles", nil)
	}
	owner := model.OwnerRole()
	if err := model.KernelCatalog().ValidateList(owner.Permissions); err != nil {
		return err
	}
	_, err := CreateRole(ctx, pool, actor, spaceID, owner)
	return err
}
