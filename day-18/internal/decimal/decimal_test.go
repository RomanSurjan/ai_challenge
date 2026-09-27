package decimal

import "testing"

func TestExactDecimalArithmetic(t *testing.T) {
	for input, want := range map[string]string{
		"001.2300": "1.23", "1e-3": "0.001", "1.25E2": "125", "-0.000": "0",
	} {
		got, err := Parse(input)
		if err != nil || got.String() != want {
			t.Fatalf("Parse(%q) = %q, %v; want %q", input, got.String(), err, want)
		}
	}
	amount := MustParse("100")
	rate := MustParse("0.923456789123456789")
	if got := amount.Mul(rate).String(); got != "92.3456789123456789" {
		t.Fatalf("conversion = %s", got)
	}
	change, err := PercentChange(MustParse("0.8"), MustParse("1"), 12)
	if err != nil || change.String() != "25" {
		t.Fatalf("percentage = %s, %v", change.String(), err)
	}
}
