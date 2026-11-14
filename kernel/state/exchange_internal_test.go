package state

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// TestTranslateExchangeError maps each database failure of the ledger to its
// typed error, also when the failure comes wrapped from a commit.
func TestTranslateExchangeError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		state string
		rule  string
		want  model.Code
	}{
		{"repeated key", "23505", "exchanges_idempotency_key", model.CodeRuleViolation},
		{"second reversal", "23505", "exchanges_reverses_once_key", model.CodeAlreadyDone},
		{"actor outside the space", "23503", "exchanges_actor_fkey", model.CodeRuleViolation},
		{"account reference", "23503", "entries_account_fkey", model.CodeRuleViolation},
		{"reversed exchange missing", "23503", "exchanges_reverses_fkey", model.CodeNotFound},
		{"space missing", "23503", "exchanges_space_id_fkey", model.CodeNotFound},
		{"entry space missing", "23503", "entries_space_id_fkey", model.CodeNotFound},
		{"closed account", "23000", "entries_account_open", model.CodeRuleViolation},
		{"account of another resource", "23000", "entries_account_resource", model.CodeRuleViolation},
		{"committed exchange", "23000", "entries_same_transaction", model.CodeRuleViolation},
		{"conservation", "23000", "exchanges_conservation", model.CodeRuleViolation},
		{"minimum entries", "23000", "exchanges_min_entries", model.CodeRuleViolation},
		{"mirror", "23000", "exchanges_reversal_mirror", model.CodeRuleViolation},
		{"scale", "23000", "entries_amount_scale", model.CodeInvalidInput},
		{"too many entries", "23000", "exchanges_max_entries", model.CodeInvalidInput},
		{"check", "23514", "entries_amount_check", model.CodeInvalidInput},
		{"malformed text", "22P02", "", model.CodeInvalidInput},
		{"no space", "23502", "", model.CodeRuleViolation},
	}
	for _, tc := range cases {
		cause := fmt.Errorf("commit transaction: %w", &pgconn.PgError{Code: tc.state, ConstraintName: tc.rule})
		got, ok := model.CodeOf(translateExchangeError("record exchange", cause))
		if !ok || got != tc.want {
			t.Errorf("%s: code = %q (typed %v), want %q", tc.name, got, ok, tc.want)
		}
	}
	other := errors.New("connection reset")
	if _, typed := model.CodeOf(translateExchangeError("record exchange", other)); typed {
		t.Error("an unknown error must stay untyped")
	}
	if !errors.Is(translateExchangeError("record exchange", other), other) {
		t.Error("an unknown error must stay wrapped")
	}
	unknown := &pgconn.PgError{Code: "23000", ConstraintName: "something_else"}
	if _, typed := model.CodeOf(translateExchangeError("record exchange", unknown)); typed {
		t.Error("an unknown constraint must stay untyped")
	}
}
