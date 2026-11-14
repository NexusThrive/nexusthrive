package model_test

import (
	"strings"
	"testing"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

func mustAmount(t *testing.T, text string) model.Amount {
	t.Helper()
	amount, err := model.ParseAmount(text)
	if err != nil {
		t.Fatalf("ParseAmount(%q) = %v", text, err)
	}
	return amount
}

// TestAmountParseAcceptsExactDecimals guards criterion 6 on the Go side.
func TestAmountParseAcceptsExactDecimals(t *testing.T) {
	t.Parallel()
	max := strings.Repeat("9", 30) + "." + strings.Repeat("9", 18)
	for text, want := range map[string]string{
		"0":         "0",
		"-0":        "0",
		"50":        "50",
		"50.00":     "50",
		"-50.00":    "-50",
		"0.5":       "0.5",
		"10.100":    "10.1",
		"-0.000001": "-0.000001",
		"100":       "100",
		max:         max,
	} {
		if got := mustAmount(t, text).String(); got != want {
			t.Errorf("ParseAmount(%q).String() = %q, want %q", text, got, want)
		}
	}
}

// TestAmountParseRejectsMalformedText guards criterion 6: no exponent, no
// plus sign, no NaN, no stray spaces, nothing beyond 30 integer or 18 decimal
// digits.
func TestAmountParseRejectsMalformedText(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"", " ", "1e3", "1E3", "+1", "NaN", "Infinity", "-Infinity", "1.", ".5", "-.5", "01", "00",
		" 1", "1 ", "1,5", "0x10", "--1", "1.2.3", "1_000",
		strings.Repeat("9", 31),
		"1." + strings.Repeat("0", 19),
	} {
		_, err := model.ParseAmount(text)
		requireCode(t, err, model.CodeInvalidInput)
	}
}

// TestAmountArithmeticIsExact shows that 0.1 + 0.2 is 0.3, which float64 gets
// wrong.
func TestAmountArithmeticIsExact(t *testing.T) {
	t.Parallel()
	sum := mustAmount(t, "0.1").Add(mustAmount(t, "0.2"))
	if got := sum.String(); got != "0.3" {
		t.Errorf("0.1 + 0.2 = %q, want 0.3", got)
	}
	if !sum.Equal(mustAmount(t, "0.30")) {
		t.Errorf("0.3 must equal 0.30")
	}
	if got := mustAmount(t, "50.25").Add(mustAmount(t, "-50.25")); !got.IsZero() || got.Sign() != 0 {
		t.Errorf("50.25 + -50.25 = %q, want zero", got.String())
	}
}

func TestAmountNegSignAndZero(t *testing.T) {
	t.Parallel()
	a := mustAmount(t, "12.5")
	if got := a.Neg().String(); got != "-12.5" {
		t.Errorf("Neg = %q, want -12.5", got)
	}
	if a.Neg().Neg().String() != "12.5" {
		t.Errorf("double Neg must return the original")
	}
	if a.Sign() != 1 || a.Neg().Sign() != -1 {
		t.Errorf("Sign() wrong: %d, %d", a.Sign(), a.Neg().Sign())
	}
	var zero model.Amount
	if !zero.IsZero() || zero.Sign() != 0 || zero.String() != "0" || zero.Neg().String() != "0" {
		t.Errorf("the zero value must be the amount 0")
	}
	if got := zero.Add(a).String(); got != "12.5" {
		t.Errorf("0 + 12.5 = %q", got)
	}
}

func TestAmountFitsScale(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		text  string
		scale int
		want  bool
	}{
		{"10.10", 2, true},
		{"10.100", 2, true},
		{"10.001", 2, false},
		{"10", 0, true},
		{"10.5", 0, false},
		{"0.000000000000000001", 18, true},
		{"0.000000000000000001", 17, false},
		{"-1.5", 1, true},
	} {
		if got := mustAmount(t, tc.text).FitsScale(tc.scale); got != tc.want {
			t.Errorf("%s FitsScale(%d) = %v, want %v", tc.text, tc.scale, got, tc.want)
		}
	}
}

// TestAmountAddDoesNotAliasOperands guards against sharing the big integers
// of the operands.
func TestAmountAddDoesNotAliasOperands(t *testing.T) {
	t.Parallel()
	a := mustAmount(t, "1.5")
	b := mustAmount(t, "2.25")
	_ = a.Add(b)
	_ = a.Neg()
	if a.String() != "1.5" || b.String() != "2.25" {
		t.Errorf("operands changed: %s, %s", a, b)
	}
}
