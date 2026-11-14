package capability_test

import (
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// TestAuthorizerExposesCatalogAndClock proves the engine can take the catalog
// and the clock from the authorizer, so the clock that verifies is the clock
// that claims and fires, and that extending the returned catalog does not
// change the authorizer.
func TestAuthorizerExposesCatalogAndClock(t *testing.T) {
	t.Parallel()
	w := newWorld(t)

	if w.auth.Clock() != model.Clock(w.clock) {
		t.Error("Clock() is not the clock the authorizer was built with")
	}
	got := w.auth.Catalog()
	if want := model.KernelCatalog().Keys(); len(got.Keys()) != len(want) {
		t.Fatalf("Catalog() has %d keys, want %d", len(got.Keys()), len(want))
	}

	extended, err := got.Extend("test", []model.PermissionDef{{Key: "test.thing.write", Risk: model.RiskWrite}})
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if _, ok := extended.Lookup("test.thing.write"); !ok {
		t.Fatal("the extended copy lacks its new key")
	}
	if _, ok := w.auth.Catalog().Lookup("test.thing.write"); ok {
		t.Error("extending the returned catalog changed the authorizer")
	}
	requireCode(t, "unknown key stays unknown", w.check(w.spaceA, system, req("test.thing.write")), model.CodeInvalidInput)
}
