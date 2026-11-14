package model_test

import (
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// TestResourceKindValid guards criterion 2: only the four kinds are valid.
func TestResourceKindValid(t *testing.T) {
	t.Parallel()
	for kind, want := range map[model.ResourceKind]bool{
		model.ResourceKindMoney: true, model.ResourceKindUnit: true,
		model.ResourceKindTime: true, model.ResourceKindAccess: true,
		"": false, "foo": false, "MONEY": false,
	} {
		if got := kind.Valid(); got != want {
			t.Errorf("ResourceKind(%q).Valid() = %v, want %v", kind, got, want)
		}
	}
}

// TestValidResourceCode checks the shape of a resource code.
func TestValidResourceCode(t *testing.T) {
	t.Parallel()
	for code, want := range map[string]bool{
		"COP": true, "UND": true, "MIN": true, "A": true, "POINTS_2": true,
		"cop": false, "": false, "1AB": false, "A B": false, "_A": false,
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA":  true,
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA": false,
	} {
		if got := model.ValidResourceCode(code); got != want {
			t.Errorf("ValidResourceCode(%q) = %v, want %v", code, got, want)
		}
	}
}

// TestValidResourceScale checks the decimals range of a resource.
func TestValidResourceScale(t *testing.T) {
	t.Parallel()
	for scale, want := range map[int]bool{-1: false, 0: true, 2: true, 18: true, 19: false} {
		if got := model.ValidResourceScale(scale); got != want {
			t.Errorf("ValidResourceScale(%d) = %v, want %v", scale, got, want)
		}
	}
}

// TestAccountClassValid guards criterion 3: only the five classes are valid.
func TestAccountClassValid(t *testing.T) {
	t.Parallel()
	for class, want := range map[model.AccountClass]bool{
		model.AccountClassAsset: true, model.AccountClassLiability: true, model.AccountClassEquity: true,
		model.AccountClassIncome: true, model.AccountClassExpense: true,
		"": false, "foo": false, "ASSET": false,
	} {
		if got := class.Valid(); got != want {
			t.Errorf("AccountClass(%q).Valid() = %v, want %v", class, got, want)
		}
	}
}

// TestAccountStatusValid checks the two account statuses.
func TestAccountStatusValid(t *testing.T) {
	t.Parallel()
	for status, want := range map[model.AccountStatus]bool{
		model.AccountStatusOpen: true, model.AccountStatusClosed: true, "": false, "frozen": false,
	} {
		if got := status.Valid(); got != want {
			t.Errorf("AccountStatus(%q).Valid() = %v, want %v", status, got, want)
		}
	}
}
