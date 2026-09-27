// Package decimal implements the small, exact base-10 arithmetic surface the
// application needs. It deliberately avoids binary floating-point values.
package decimal

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

var pattern = regexp.MustCompile(`^([+-]?)([0-9]+)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$`)

// Decimal is an arbitrary-precision integer coefficient with a decimal scale.
type Decimal struct {
	coefficient *big.Int
	scale       int
}

func Parse(value string) (Decimal, error) {
	value = strings.TrimSpace(value)
	match := pattern.FindStringSubmatch(value)
	if match == nil {
		return Decimal{}, fmt.Errorf("invalid decimal %q", value)
	}
	digits := strings.TrimLeft(match[2]+match[3], "0")
	if digits == "" {
		digits = "0"
	}
	coefficient, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return Decimal{}, errors.New("invalid decimal coefficient")
	}
	if match[1] == "-" {
		coefficient.Neg(coefficient)
	}
	exponent := 0
	if match[4] != "" {
		if _, err := fmt.Sscanf(match[4], "%d", &exponent); err != nil {
			return Decimal{}, fmt.Errorf("invalid decimal exponent: %w", err)
		}
	}
	scale := len(match[3]) - exponent
	if scale < 0 {
		coefficient.Mul(coefficient, pow10(-scale))
		scale = 0
	}
	return normalize(Decimal{coefficient: coefficient, scale: scale}), nil
}

func MustParse(value string) Decimal {
	d, err := Parse(value)
	if err != nil {
		panic(err)
	}
	return d
}

func (d Decimal) String() string {
	d = d.valid()
	negative := d.coefficient.Sign() < 0
	digits := new(big.Int).Abs(d.coefficient).String()
	if d.scale > 0 {
		if len(digits) <= d.scale {
			digits = strings.Repeat("0", d.scale-len(digits)+1) + digits
		}
		index := len(digits) - d.scale
		digits = digits[:index] + "." + digits[index:]
	}
	if negative && digits != "0" {
		return "-" + digits
	}
	return digits
}

func (d Decimal) Sign() int { return d.valid().coefficient.Sign() }

func (d Decimal) Cmp(other Decimal) int {
	a, b := align(d.valid(), other.valid())
	return a.Cmp(b)
}

func (d Decimal) Sub(other Decimal) Decimal {
	a, b := align(d.valid(), other.valid())
	return normalize(Decimal{coefficient: new(big.Int).Sub(a, b), scale: max(d.scale, other.scale)})
}

func (d Decimal) Mul(other Decimal) Decimal {
	d, other = d.valid(), other.valid()
	return normalize(Decimal{coefficient: new(big.Int).Mul(d.coefficient, other.coefficient), scale: d.scale + other.scale})
}

// Div returns a value rounded half away from zero to precision fractional digits.
func (d Decimal) Div(other Decimal, precision int) (Decimal, error) {
	d, other = d.valid(), other.valid()
	if other.coefficient.Sign() == 0 {
		return Decimal{}, errors.New("division by zero")
	}
	if precision < 0 || precision > 100 {
		return Decimal{}, errors.New("precision must be between 0 and 100")
	}
	numerator := new(big.Int).Mul(d.coefficient, pow10(other.scale+precision))
	denominator := new(big.Int).Mul(other.coefficient, pow10(d.scale))
	quotient, remainder := new(big.Int).QuoRem(numerator, denominator, new(big.Int))
	if remainder.Sign() != 0 {
		doubleRemainder := new(big.Int).Abs(remainder)
		doubleRemainder.Mul(doubleRemainder, big.NewInt(2))
		if doubleRemainder.Cmp(new(big.Int).Abs(denominator)) >= 0 {
			if numerator.Sign()*denominator.Sign() < 0 {
				quotient.Sub(quotient, big.NewInt(1))
			} else {
				quotient.Add(quotient, big.NewInt(1))
			}
		}
	}
	return normalize(Decimal{coefficient: quotient, scale: precision}), nil
}

func PercentChange(first, last Decimal, precision int) (Decimal, error) {
	if first.Sign() == 0 {
		return Decimal{}, errors.New("cannot calculate percentage change from zero")
	}
	return last.Sub(first).Mul(MustParse("100")).Div(first, precision)
}

func align(a, b Decimal) (*big.Int, *big.Int) {
	scale := max(a.scale, b.scale)
	left := new(big.Int).Set(a.coefficient)
	right := new(big.Int).Set(b.coefficient)
	left.Mul(left, pow10(scale-a.scale))
	right.Mul(right, pow10(scale-b.scale))
	return left, right
}

func normalize(d Decimal) Decimal {
	if d.coefficient == nil {
		d.coefficient = new(big.Int)
	}
	ten := big.NewInt(10)
	zero := new(big.Int)
	for d.scale > 0 {
		q, r := new(big.Int).QuoRem(d.coefficient, ten, new(big.Int))
		if r.Cmp(zero) != 0 {
			break
		}
		d.coefficient = q
		d.scale--
	}
	return d
}

func (d Decimal) valid() Decimal {
	if d.coefficient == nil {
		return Decimal{coefficient: new(big.Int)}
	}
	return d
}

func pow10(n int) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }
