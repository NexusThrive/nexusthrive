package model_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

const (
	testDebtorID   = "01900000-0000-7000-8000-0000000000d1"
	testCreditorID = "01900000-0000-7000-8000-0000000000d2"
)

// validCommitment returns a commitment that passes validation, for tests to break.
func validCommitment(t *testing.T) model.NewCommitment {
	t.Helper()
	return model.NewCommitment{
		Kind: "test.commitment", DebtorParticipantID: testDebtorID, CreditorParticipantID: testCreditorID,
		ResourceID: testResourceID, Amount: mustAmount(t, "100.00"),
		DueAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestNewCommitmentValidateAcceptsValidCommitment(t *testing.T) {
	t.Parallel()
	in := validCommitment(t)
	if err := in.Validate(); err != nil {
		t.Errorf("Validate() of the minimal commitment = %v, want nil", err)
	}
	in.OriginRef = "test.origin/1"
	in.StartsAt = in.DueAt // a commitment due the instant it starts is allowed
	if err := in.Validate(); err != nil {
		t.Errorf("Validate() with origin and start = %v, want nil", err)
	}
}

// TestNewCommitmentValidateRejectsBadInput guards criterion 3: an amount of
// zero or less, a due date before the start, equal parties and a missing due
// date are all invalid_input.
func TestNewCommitmentValidateRejectsBadInput(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*model.NewCommitment){
		"zero amount":      func(c *model.NewCommitment) { c.Amount = mustAmount(t, "0.00") },
		"unset amount":     func(c *model.NewCommitment) { c.Amount = model.Amount{} },
		"negative amount":  func(c *model.NewCommitment) { c.Amount = mustAmount(t, "-1") },
		"due before start": func(c *model.NewCommitment) { c.StartsAt = c.DueAt.Add(time.Second) },
		"same parties":     func(c *model.NewCommitment) { c.CreditorParticipantID = c.DebtorParticipantID },
		"no due date":      func(c *model.NewCommitment) { c.DueAt = time.Time{} },
		"empty kind":       func(c *model.NewCommitment) { c.Kind = "" },
		"single segment":   func(c *model.NewCommitment) { c.Kind = "commitment" },
		"uppercase kind":   func(c *model.NewCommitment) { c.Kind = "Test.Commitment" },
		"long kind":        func(c *model.NewCommitment) { c.Kind = "test." + strings.Repeat("a", 130) },
		"bad debtor":       func(c *model.NewCommitment) { c.DebtorParticipantID = "nope" },
		"nil debtor":       func(c *model.NewCommitment) { c.DebtorParticipantID = "00000000-0000-0000-0000-000000000000" },
		"empty creditor":   func(c *model.NewCommitment) { c.CreditorParticipantID = "" },
		"bad resource":     func(c *model.NewCommitment) { c.ResourceID = "" },
		"blank origin":     func(c *model.NewCommitment) { c.OriginRef = "   " },
		"long origin":      func(c *model.NewCommitment) { c.OriginRef = strings.Repeat("o", 256) },
	}
	for name, mutate := range cases {
		in := validCommitment(t)
		mutate(&in)
		if err := in.Validate(); err == nil {
			t.Errorf("%s: Validate() = nil, want invalid_input", name)
		} else {
			requireCode(t, err, model.CodeInvalidInput)
		}
	}
}

func TestValidateCancelReason(t *testing.T) {
	t.Parallel()
	if err := model.ValidateCancelReason("creditor asked"); err != nil {
		t.Errorf("ValidateCancelReason(valid) = %v", err)
	}
	for name, reason := range map[string]string{"empty": "", "blank": "  \t", "long": strings.Repeat("r", 501)} {
		requireCode(t, model.ValidateCancelReason(reason), model.CodeInvalidInput)
		_ = name
	}
}

func TestCommitmentStatusValid(t *testing.T) {
	t.Parallel()
	for _, s := range []model.CommitmentStatus{model.CommitmentStatusOpen, model.CommitmentStatusFulfilled,
		model.CommitmentStatusCanceled, model.CommitmentStatusExpired} {
		if !s.Valid() {
			t.Errorf("%q must be valid", s)
		}
	}
	if model.CommitmentStatus("late").Valid() || model.CommitmentStatus("").Valid() {
		t.Error("unknown statuses must be invalid")
	}
}
