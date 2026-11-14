package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

const (
	testAccountID  = "01900000-0000-7000-8000-0000000000a1"
	testAccountID2 = "01900000-0000-7000-8000-0000000000a2"
	testResourceID = "01900000-0000-7000-8000-0000000000b1"
)

// validExchange returns an exchange that passes validation, for tests to break.
func validExchange(t *testing.T) model.NewExchange {
	t.Helper()
	return model.NewExchange{
		Kind:           "test.exchange",
		IdempotencyKey: "key-1",
		Entries: []model.NewEntry{
			{AccountID: testAccountID, ResourceID: testResourceID, Amount: mustAmount(t, "50.00")},
			{AccountID: testAccountID2, ResourceID: testResourceID, Amount: mustAmount(t, "-50.00")},
		},
	}
}

func TestNewExchangeValidateAcceptsValidExchange(t *testing.T) {
	t.Parallel()
	in := validExchange(t)
	in.Memo = "note"
	in.Metadata = json.RawMessage(`{"k":"v"}`)
	in.ReversesExchangeID = "01900000-0000-7000-8000-0000000000c1"
	if err := in.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
	if err := validExchange(t).Validate(); err != nil {
		t.Errorf("Validate() of the minimal exchange = %v, want nil", err)
	}
}

// TestNewExchangeValidateRejectsFewOrZeroEntries guards criterion 4.
func TestNewExchangeValidateRejectsFewOrZeroEntries(t *testing.T) {
	t.Parallel()
	empty := validExchange(t)
	empty.Entries = nil
	single := validExchange(t)
	single.Entries = single.Entries[:1]
	zero := validExchange(t)
	zero.Entries[0].Amount = mustAmount(t, "0.00")
	zero.Entries[1].Amount = mustAmount(t, "0")
	var unset model.Amount
	unsetAmount := validExchange(t)
	unsetAmount.Entries[0].Amount = unset
	for _, in := range map[string]model.NewExchange{
		"no entries": empty, "one entry": single, "zero amounts": zero, "unset amount": unsetAmount,
	} {
		requireCode(t, in.Validate(), model.CodeInvalidInput)
	}
}

func TestNewExchangeValidateBoundsEntryCount(t *testing.T) {
	t.Parallel()
	build := func(n int) model.NewExchange {
		in := validExchange(t)
		in.Entries = nil
		for i := 0; i < n; i++ {
			amount := "1"
			if i%2 == 1 {
				amount = "-1"
			}
			in.Entries = append(in.Entries, model.NewEntry{AccountID: testAccountID, ResourceID: testResourceID, Amount: mustAmount(t, amount)})
		}
		return in
	}
	if err := build(100).Validate(); err != nil {
		t.Errorf("100 entries: %v, want nil", err)
	}
	requireCode(t, build(101).Validate(), model.CodeInvalidInput)
}

func TestNewExchangeValidateRejectsMalformedFields(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*model.NewExchange){
		"empty kind":       func(e *model.NewExchange) { e.Kind = "" },
		"one segment kind": func(e *model.NewExchange) { e.Kind = "exchange" },
		"uppercase kind":   func(e *model.NewExchange) { e.Kind = "Test.Exchange" },
		"long kind":        func(e *model.NewExchange) { e.Kind = "test." + strings.Repeat("a", 130) },
		"empty key":        func(e *model.NewExchange) { e.IdempotencyKey = "" },
		"blank key":        func(e *model.NewExchange) { e.IdempotencyKey = "   " },
		"long key":         func(e *model.NewExchange) { e.IdempotencyKey = strings.Repeat("k", 256) },
		"blank memo":       func(e *model.NewExchange) { e.Memo = "  " },
		"array metadata":   func(e *model.NewExchange) { e.Metadata = json.RawMessage(`[1]`) },
		"null metadata":    func(e *model.NewExchange) { e.Metadata = json.RawMessage(`null`) },
		"broken metadata":  func(e *model.NewExchange) { e.Metadata = json.RawMessage(`{"k":`) },
		"bad reverses":     func(e *model.NewExchange) { e.ReversesExchangeID = "nope" },
		"nil reverses":     func(e *model.NewExchange) { e.ReversesExchangeID = "00000000-0000-0000-0000-000000000000" },
		"bad account":      func(e *model.NewExchange) { e.Entries[0].AccountID = "nope" },
		"empty account":    func(e *model.NewExchange) { e.Entries[1].AccountID = "" },
		"nil resource":     func(e *model.NewExchange) { e.Entries[0].ResourceID = "00000000-0000-0000-0000-000000000000" },
		"empty resource":   func(e *model.NewExchange) { e.Entries[1].ResourceID = "" },
	}
	for name, mutate := range cases {
		in := validExchange(t)
		mutate(&in)
		if err := in.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want invalid_input", name)
			continue
		}
		requireCode(t, in.Validate(), model.CodeInvalidInput)
	}
}
