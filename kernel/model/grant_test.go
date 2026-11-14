package model_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// TestGrantIsActiveRespectsExpiryAndRevocation guards criterion 3 with a fake
// clock: a grant is active only while it is unrevoked and before its expiry.
func TestGrantIsActiveRespectsExpiryAndRevocation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }

	cases := map[string]struct {
		grant model.Grant
		want  bool
	}{
		"no expiry":              {model.Grant{}, true},
		"future expiry":          {model.Grant{ExpiresAt: at(time.Second)}, true},
		"expiry equal to now":    {model.Grant{ExpiresAt: at(0)}, false},
		"past expiry":            {model.Grant{ExpiresAt: at(-time.Second)}, false},
		"revoked, no expiry":     {model.Grant{RevokedAt: at(-time.Hour)}, false},
		"revoked, future expiry": {model.Grant{RevokedAt: at(-time.Hour), ExpiresAt: at(time.Hour)}, false},
	}
	for name, tc := range cases {
		if got := tc.grant.IsActive(now); got != tc.want {
			t.Errorf("%s: IsActive = %v, want %v", name, got, tc.want)
		}
	}
}

// TestSystemClockReturnsUTC checks that the production clock is in UTC.
func TestSystemClockReturnsUTC(t *testing.T) {
	t.Parallel()
	var clock model.Clock = model.SystemClock{}
	got := clock.Now()
	if got.Location() != time.UTC {
		t.Errorf("location = %v, want UTC", got.Location())
	}
	if time.Since(got) > time.Minute || time.Until(got) > time.Minute {
		t.Errorf("SystemClock.Now = %v, far from the real time", got)
	}
}

// TestValidRoleKey guards criterion 1: the key is lowercase with underscores.
func TestValidRoleKey(t *testing.T) {
	t.Parallel()
	for key, want := range map[string]bool{
		"owner": true, "front_desk": true, "a": true, "r2": true, strings.Repeat("a", 64): true,
		"": false, "Owner": false, "2fast": false, "a b": false, "a-b": false, strings.Repeat("a", 65): false,
	} {
		if got := model.ValidRoleKey(key); got != want {
			t.Errorf("ValidRoleKey(%q) = %v, want %v", key, got, want)
		}
	}
}

// TestValidPermissionKey checks the shape shared by permissions and
// capability keys: two or more lowercase segments separated by dots.
func TestValidPermissionKey(t *testing.T) {
	t.Parallel()
	for key, want := range map[string]bool{
		"kernel.test.read": true, "test.capability": true, "a.b": true, "a_b.c_d": true,
		"": false, "single": false, "Test.read": false, "test..read": false,
		"test.read.": false, ".test.read": false, "test.Read": false, "test. read": false, "a,b.c": false,
	} {
		if got := model.ValidPermissionKey(key); got != want {
			t.Errorf("ValidPermissionKey(%q) = %v, want %v", key, got, want)
		}
	}
}

// TestValidPermissionList checks the list rules of roles and grants.
func TestValidPermissionList(t *testing.T) {
	t.Parallel()
	tooMany := make([]string, model.MaxPermissions+1)
	for i := range tooMany {
		tooMany[i] = "test.read"
	}
	if !model.ValidPermissionList(nil) || !model.ValidPermissionList([]string{}) {
		t.Error("an empty list is valid")
	}
	if !model.ValidPermissionList(tooMany[:model.MaxPermissions]) {
		t.Error("a list at the limit is valid")
	}
	if model.ValidPermissionList(tooMany) {
		t.Error("a list over the limit is invalid")
	}
	if model.ValidPermissionList([]string{"test.read", "Bad"}) {
		t.Error("a malformed element is invalid")
	}
}

// TestCapabilitySourceAndAccessActionValid checks the closed sets.
func TestCapabilitySourceAndAccessActionValid(t *testing.T) {
	t.Parallel()
	for _, s := range []model.CapabilitySource{
		model.CapabilitySourcePackage, model.CapabilitySourceAI, model.CapabilitySourceAdmin, model.CapabilitySourceCode,
	} {
		if !s.Valid() {
			t.Errorf("source %q must be valid", s)
		}
	}
	for _, s := range []model.CapabilitySource{"", "owner", "Package"} {
		if s.Valid() {
			t.Errorf("source %q must be invalid", s)
		}
	}
	if !model.AccessActionRead.Valid() || !model.AccessActionWrite.Valid() {
		t.Error("read and write are valid actions")
	}
	if model.AccessAction("delete").Valid() || model.AccessAction("").Valid() {
		t.Error("other actions are invalid")
	}
}
