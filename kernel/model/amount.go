package model

import (
	"math/big"
	"regexp"
	"strings"
)

// amountPattern is the only text form ParseAmount accepts: an optional minus
// sign, up to 30 integer digits without leading zeros and up to 18 decimals.
// It has no plus sign, exponent, NaN, infinity or spaces. The bounds match the
// range of kernel.entries.amount and the largest scale of a resource.
var amountPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,29})(\.[0-9]{1,18})?$`)

// ten is the base used to align the scales of two amounts.
var ten = big.NewInt(10)

// Amount is an exact decimal number: an integer and the count of decimals it
// carries. Money never goes through float64. The value is normalized, with no
// trailing zeros, so 50.00 and 50 are the same Amount; the zero value is the
// amount 0. Amount is immutable: every operation returns a new value.
type Amount struct {
	// unscaled is the integer digits of the amount; nil means zero.
	unscaled *big.Int
	// scale is how many digits of unscaled lie after the decimal point.
	scale int
}

// ParseAmount reads a decimal amount such as "-50.25". Anything outside
// ^-?(0|[1-9][0-9]{0,29})(\.[0-9]{1,18})?$ is invalid_input.
func ParseAmount(text string) (Amount, error) {
	if !amountPattern.MatchString(text) {
		return Amount{}, NewError(CodeInvalidInput, "amount must be a plain decimal with at most 30 integer and 18 decimal digits", nil)
	}
	digits := strings.TrimPrefix(text, "-")
	scale := 0
	if dot := strings.IndexByte(digits, '.'); dot >= 0 {
		scale = len(digits) - dot - 1
		digits = digits[:dot] + digits[dot+1:]
	}
	unscaled, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return Amount{}, NewError(CodeInvalidInput, "amount is not a number", nil)
	}
	if strings.HasPrefix(text, "-") {
		unscaled.Neg(unscaled)
	}
	return normalizeAmount(unscaled, scale), nil
}

// normalizeAmount strips trailing zeros so equal values have one form. It
// takes ownership of unscaled.
func normalizeAmount(unscaled *big.Int, scale int) Amount {
	if unscaled.Sign() == 0 {
		return Amount{}
	}
	remainder := new(big.Int)
	quotient := new(big.Int)
	for scale > 0 {
		quotient.QuoRem(unscaled, ten, remainder)
		if remainder.Sign() != 0 {
			break
		}
		unscaled.Set(quotient)
		scale--
	}
	return Amount{unscaled: unscaled, scale: scale}
}

// digits returns the unscaled integer, never nil and never shared.
func (a Amount) digits() *big.Int {
	if a.unscaled == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(a.unscaled)
}

// String returns the plain decimal form without trailing zeros, such as
// "-50.25" or "0".
func (a Amount) String() string {
	if a.unscaled == nil || a.unscaled.Sign() == 0 {
		return "0"
	}
	negative := a.unscaled.Sign() < 0
	text := new(big.Int).Abs(a.unscaled).String()
	if a.scale > 0 {
		if len(text) <= a.scale {
			text = strings.Repeat("0", a.scale-len(text)+1) + text
		}
		point := len(text) - a.scale
		text = text[:point] + "." + text[point:]
	}
	if negative {
		return "-" + text
	}
	return text
}

// Neg returns the amount with the opposite sign.
func (a Amount) Neg() Amount {
	return Amount{unscaled: a.digits().Neg(a.digits()), scale: a.scale}
}

// Add returns the exact sum of a and b.
func (a Amount) Add(b Amount) Amount {
	scale := max(a.scale, b.scale)
	left := a.digits().Mul(a.digits(), pow10(scale-a.scale))
	right := b.digits().Mul(b.digits(), pow10(scale-b.scale))
	return normalizeAmount(left.Add(left, right), scale)
}

// pow10 returns 10 to the power of exponent as a new integer.
func pow10(exponent int) *big.Int {
	return new(big.Int).Exp(ten, big.NewInt(int64(exponent)), nil)
}

// Sign returns -1, 0 or 1 for a negative, zero or positive amount.
func (a Amount) Sign() int {
	if a.unscaled == nil {
		return 0
	}
	return a.unscaled.Sign()
}

// IsZero reports whether the amount is zero.
func (a Amount) IsZero() bool { return a.Sign() == 0 }

// Equal reports whether a and b are the same value.
func (a Amount) Equal(b Amount) bool {
	return a.scale == b.scale && a.digits().Cmp(b.digits()) == 0
}

// FitsScale reports whether the amount has at most scale decimals once
// trailing zeros are ignored, so 10.100 fits a scale of 2.
func (a Amount) FitsScale(scale int) bool {
	return a.scale <= scale
}
