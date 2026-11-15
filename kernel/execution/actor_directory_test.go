package execution

import (
	"context"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

func TestActorDirectoryResolvesOnlyActiveParticipantWithoutWrites(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	identity, err := state.CreateIdentity(context.Background(), w.api, system, state.NewIdentity{Provider: model.IdentityProviderClerk, Subject: "directory-person", DisplayName: "Person"})
	if err != nil {
		t.Fatal(err)
	}
	participant, err := state.CreateParticipant(context.Background(), w.api, system, w.spaceA, state.NewParticipant{Kind: model.ParticipantKindPerson, IdentityID: identity, DisplayName: "Person"})
	if err != nil {
		t.Fatal(err)
	}
	actor := model.Actor{Kind: model.ActorPerson, ParticipantID: participant}
	grant := w.grantRole(w.spaceA, actor, "reader", nil, model.PermissionEventRead)
	before := w.snapshot()
	resolved, err := w.engine.ResolveActorForSlug(context.Background(), "world-a", model.IdentityProviderClerk, "directory-person")
	if err != nil || resolved.SpaceID != w.spaceA || resolved.IdentityID != identity || resolved.Actor != actor {
		t.Fatalf("resolution=%+v, error=%v", resolved, err)
	}
	for _, tc := range []struct{ slug, subject string }{{"world-b", "directory-person"}, {"missing", "directory-person"}, {"world-a", "unknown"}, {"../world-a", "directory-person"}} {
		_, err := w.engine.ResolveActorForSlug(context.Background(), tc.slug, model.IdentityProviderClerk, tc.subject)
		requireCode(t, "missing participant", err, model.CodePermissionDenied)
		if err.Error() != "permission_denied: the identity has no access to this space" {
			t.Fatalf("enumerating refusal: %v", err)
		}
	}
	if changed := w.changed(before); len(changed) != 0 {
		t.Fatalf("resolution changed rows: %v", changed)
	}
	func() {
		conn, err := w.api.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(context.Background(), `SET search_path=pg_temp,public`); err != nil {
			t.Fatal(err)
		}
		defer conn.ExecContext(context.Background(), `RESET search_path`)
		for _, table := range []string{"spaces", "identities", "participants"} {
			if _, err := conn.ExecContext(context.Background(), `CREATE TEMP TABLE `+table+` (unexpected text)`); err != nil {
				t.Fatal(err)
			}
			defer conn.ExecContext(context.Background(), `DROP TABLE pg_temp.`+table)
		}
		var spaceID, identityID, participantID string
		if err := conn.QueryRowContext(context.Background(), `SELECT * FROM kernel.resolve_space_actor('clerk','directory-person','world-a')`).Scan(&spaceID, &identityID, &participantID); err != nil || spaceID != w.spaceA || identityID != identity || participantID != participant {
			t.Fatalf("temporary objects changed directory result: %v", err)
		}
	}()
	if _, err := w.admin.Exec(`UPDATE kernel.grants SET revoked_at=now() WHERE id=$1`, grant); err != nil {
		t.Fatal(err)
	}
	_, err = w.engine.ReadEvents(context.Background(), call(resolved.Actor, resolved.SpaceID), 0, 10)
	requireCode(t, "revoked after resolution", err, model.CodePermissionDenied)
	if _, err := w.admin.Exec(`UPDATE kernel.identities SET disabled_at=now() WHERE id=$1`, identity); err != nil {
		t.Fatal(err)
	}
	_, err = w.engine.ResolveActorForSlug(context.Background(), "world-a", model.IdentityProviderClerk, "directory-person")
	requireCode(t, "disabled identity", err, model.CodePermissionDenied)
	if _, err := w.admin.Exec(`UPDATE kernel.identities SET disabled_at=NULL WHERE id=$1`, identity); err != nil {
		t.Fatal(err)
	}
	if _, err := w.admin.Exec(`UPDATE kernel.participants SET status='archived',archived_at=now() WHERE id=$1`, participant); err != nil {
		t.Fatal(err)
	}
	_, err = w.engine.ResolveActorForSlug(context.Background(), "world-a", model.IdentityProviderClerk, "directory-person")
	requireCode(t, "archived participant", err, model.CodePermissionDenied)
}

func TestDirectoryPrivilegesAreLimitedToItsFixedFunction(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if got := w.count(`SELECT count(*) FROM pg_policies WHERE schemaname='kernel' AND
      (tablename,policyname) IN (('spaces','directory_spaces'),('identities','directory_identities'),('participants','directory_participants'))
      AND cmd='SELECT' AND roles=ARRAY['nexusthrive_directory']::name[] AND qual='true'`); got != 3 {
		t.Fatal("directory policies are not exclusive read policies")
	}
	if got := w.count(`SELECT count(*) FROM pg_roles WHERE rolname='nexusthrive_directory' AND NOT (rolcanlogin OR rolsuper OR rolbypassrls OR rolcreaterole OR rolcreatedb OR rolreplication)`); got != 1 {
		t.Fatal("directory role has dangerous attributes")
	}
	if got := w.count(`SELECT count(*) FROM pg_auth_members WHERE member=(SELECT oid FROM pg_roles WHERE rolname='nexusthrive_api') AND roleid=(SELECT oid FROM pg_roles WHERE rolname='nexusthrive_directory')`); got != 0 {
		t.Fatal("API can assume directory role")
	}
	if got := w.count(`SELECT count(*) FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='kernel.resolve_space_actor(text,text,text)'::regprocedure AND r.rolname='nexusthrive_directory' AND p.prosecdef AND p.proconfig @> ARRAY['search_path=pg_catalog']`); got != 1 {
		t.Fatal("directory function lacks fixed owner or search path")
	}
	if got := w.count(`SELECT count(*) FROM pg_proc p CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE p.oid='kernel.resolve_space_actor(text,text,text)'::regprocedure AND a.grantee=0 AND a.privilege_type='EXECUTE'`); got != 0 {
		t.Fatal("PUBLIC can execute directory function")
	}
	var writable bool
	if err := w.admin.QueryRow(`SELECT has_table_privilege('nexusthrive_directory','kernel.participants','INSERT,UPDATE,DELETE')`).Scan(&writable); err != nil || writable {
		t.Fatalf("directory can write: %v", err)
	}
	conn, err := w.admin.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// SET ROLE alone retains a superuser session_user in APIPool. Session
	// authorization tests the API's actual role-switching rights without LOGIN.
	if _, err := conn.ExecContext(context.Background(), `SET SESSION AUTHORIZATION nexusthrive_api`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), `RESET SESSION AUTHORIZATION`); err != nil {
			t.Error(err)
		}
	}()
	if _, err := conn.ExecContext(context.Background(), `SET ROLE nexusthrive_directory`); err == nil {
		t.Fatal("API assumed directory role")
	}
}
