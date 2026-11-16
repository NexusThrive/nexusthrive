package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
	"github.com/nexusthrive/nexusthrive/kernel/state"
)

func TestWebhookFanoutIsolationAndClaims(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	writer := w.holder(w.spaceA, "hooks", model.PermissionWebhookWrite, model.PermissionWebhookRead)
	create := func(space string, actor model.Actor) string {
		id := NewRequestID()
		err := w.engine.CreateWebhookEndpoint(ctx, call(actor, space), model.NewWebhookEndpoint{ID: id, URL: "https://example.com/hook", Events: []string{"kernel.exchange.recorded.v1"}, Secret: model.SealedWebhookSecret{KeyID: "test", Nonce: make([]byte, 12), Ciphertext: make([]byte, 48)}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	denied := w.person(w.spaceA, "no-write")
	errDenied := w.engine.CreateWebhookEndpoint(ctx, call(denied, w.spaceA), model.NewWebhookEndpoint{ID: NewRequestID(), URL: "https://example.com", Events: []string{"kernel.exchange.recorded.v1"}, Secret: model.SealedWebhookSecret{KeyID: "test", Nonce: make([]byte, 12), Ciphertext: make([]byte, 48)}})
	requireCode(t, "create permission", errDenied, model.CodePermissionDenied)
	endpoint := create(w.spaceA, writer)
	create(w.spaceB, system)
	_, err := w.engine.ListWebhookDeliveries(ctx, call(w.person(w.spaceA, "no-access"), w.spaceA), endpoint, 10)
	requireCode(t, "permission", err, model.CodePermissionDenied)
	var event model.Event
	err = state.InSpace(ctx, w.api, w.spaceA, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		event, err = state.AppendEventAndEnqueue(ctx, tx, w.clock, model.NewEvent{Type: "kernel.exchange.recorded", Version: 1, Payload: json.RawMessage(`{"exchange_id":"test"}`), Actor: system}, model.SubscriberWebhooks)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.engine.FanoutWebhooks(ctx, call(system, w.spaceA), 100); err != nil {
		t.Fatal(err)
	}
	if err := w.engine.FanoutWebhooks(ctx, call(system, w.spaceA), 100); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM kernel.webhook_deliveries`); n != 1 {
		t.Fatalf("deliveries %d", n)
	}
	job, err := w.engine.ClaimWebhookDelivery(ctx, call(system, w.spaceA))
	if err != nil || job == nil {
		t.Fatalf("claim %v %v", job, err)
	}
	var body map[string]any
	if err := json.Unmarshal(job.Body, &body); err != nil || body["id"] != event.ID {
		t.Fatalf("envelope %s %v", job.Body, err)
	}
	if other, err := w.engine.ClaimWebhookDelivery(ctx, call(system, w.spaceA)); err != nil || other != nil {
		t.Fatal("duplicate claim", other, err)
	}
	err = w.engine.FinishWebhookDelivery(ctx, call(system, w.spaceB), *job, 200, 1, true, true)
	requireCode(t, "cross-space finish", err, model.CodeNotFound)
	if err := w.engine.FinishWebhookDelivery(ctx, call(system, w.spaceA), *job, 200, 1, true, true); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM kernel.webhook_delivery_attempts`); n != 1 {
		t.Fatalf("attempts %d", n)
	}
	if err := w.engine.FinishWebhookDelivery(ctx, call(system, w.spaceA), *job, 200, 1, true, true); err == nil {
		t.Fatal("stale finish accepted")
	}
	deliveries, err := w.engine.ListWebhookDeliveries(ctx, call(writer, w.spaceA), endpoint, 10)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != "sent" {
		t.Fatal(deliveries, err)
	}
	if _, err := w.engine.ListWebhookDeliveries(ctx, call(system, w.spaceB), endpoint, 10); err == nil {
		t.Fatal("cross-space endpoint visible")
	}
}

func TestWebhookFanoutFailureRollsBackAndLeaseFencesLateResults(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	id := NewRequestID()
	if err := w.engine.CreateWebhookEndpoint(ctx, call(system, w.spaceA), model.NewWebhookEndpoint{ID: id, URL: "https://example.com", Events: []string{"kernel.exchange.recorded.v1"}, Secret: model.SealedWebhookSecret{KeyID: "test", Nonce: make([]byte, 12), Ciphertext: make([]byte, 48)}}); err != nil {
		t.Fatal(err)
	}
	err := state.InSpace(ctx, w.api, w.spaceA, func(ctx context.Context, tx *sql.Tx) error {
		_, err := state.AppendEventAndEnqueue(ctx, tx, w.clock, model.NewEvent{Type: "kernel.exchange.recorded", Version: 1, Payload: json.RawMessage(`{}`), Actor: system}, model.SubscriberWebhooks)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.admin.Exec(`CREATE FUNCTION kernel.fail_webhook_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$; CREATE TRIGGER fail_webhook_insert BEFORE INSERT ON kernel.webhook_deliveries FOR EACH ROW EXECUTE FUNCTION kernel.fail_webhook_insert()`)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.engine.FanoutWebhooks(ctx, call(system, w.spaceA), 100); err == nil {
		t.Fatal("fanout failure hidden")
	}
	if w.count(`SELECT count(*) FROM kernel.webhook_deliveries`) != 0 || w.count(`SELECT count(*) FROM kernel.outbox WHERE status='sent'`) != 0 {
		t.Fatal("partial fanout persisted")
	}
	if _, err := w.admin.Exec(`DROP TRIGGER fail_webhook_insert ON kernel.webhook_deliveries; DROP FUNCTION kernel.fail_webhook_insert()`); err != nil {
		t.Fatal(err)
	}
	w.clock.Set(w.clock.Now().Add(model.OutboxLease + time.Second))
	if err := w.engine.FanoutWebhooks(ctx, call(system, w.spaceA), 100); err != nil {
		t.Fatal(err)
	}
	w.clock.Set(w.clock.Now().Add(model.OutboxBackoff(1) + time.Second))
	if err := w.engine.FanoutWebhooks(ctx, call(system, w.spaceA), 100); err != nil {
		t.Fatal(err)
	}
	job, err := w.engine.ClaimWebhookDelivery(ctx, call(system, w.spaceA))
	if err != nil || job == nil {
		t.Fatal(job, err)
	}
	w.clock.Set(w.clock.Now().Add(model.OutboxLease + time.Second))
	if err := w.engine.FinishWebhookDelivery(ctx, call(system, w.spaceA), *job, 200, 1, true, true); err == nil {
		t.Fatal("expired result accepted")
	}
	if _, err := w.engine.ClaimWebhookDelivery(ctx, call(system, w.spaceA)); err != nil {
		t.Fatal(err)
	}
	if w.count(`SELECT count(*) FROM kernel.webhook_delivery_attempts WHERE outcome='claim_expired'`) != 1 {
		t.Fatal("expired attempt history missing")
	}
}

func TestWebhookWorkerDirectoryIsNarrowAndInternal(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if _, err := w.engine.ListWebhookSpaces(ctx, w.person(w.spaceA, "person"), "", 10); err == nil {
		t.Fatal("participant listed spaces")
	}
	spaces, err := w.engine.ListWebhookSpaces(ctx, system, "", 1)
	if err != nil || len(spaces) != 1 {
		t.Fatal(spaces, err)
	}
	next, err := w.engine.ListWebhookSpaces(ctx, system, spaces[0], 1)
	if err != nil || len(next) != 1 || next[0] == spaces[0] {
		t.Fatal(next, err)
	}
	w.setStatus(next[0], "suspended")
	next, err = w.engine.ListWebhookSpaces(ctx, system, spaces[0], 10)
	if err != nil || len(next) != 0 {
		t.Fatal(next, err)
	}
	var unsafe bool
	if err := w.admin.QueryRow(`SELECT rolcanlogin OR rolsuper OR rolbypassrls OR rolinherit OR rolcreatedb OR rolcreaterole OR rolreplication FROM pg_roles WHERE rolname='nexusthrive_worker_directory'`).Scan(&unsafe); err != nil || unsafe {
		t.Fatal("unsafe worker role", err)
	}
	conn, err := w.admin.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET SESSION AUTHORIZATION nexusthrive_api`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.ExecContext(ctx, `RESET SESSION AUTHORIZATION`); err != nil {
			t.Error(err)
		}
	}()
	if _, err := conn.ExecContext(ctx, `SET ROLE nexusthrive_worker_directory`); err == nil {
		t.Fatal("API assumed worker directory role")
	}
	var public bool
	if err := w.admin.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_proc p, LATERAL aclexplode(COALESCE(p.proacl,acldefault('f',p.proowner))) a WHERE p.oid='kernel.list_webhook_spaces(uuid,integer)'::regprocedure AND a.grantee=0 AND a.privilege_type='EXECUTE')`).Scan(&public); err != nil || public {
		t.Fatal("public worker directory", err)
	}
}
