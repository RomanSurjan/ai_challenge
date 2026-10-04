package evaluation

import "testing"

func TestComputeMetrics(t *testing.T) {
	metrics := ComputeMetrics([][]bool{
		{true, false, false, false, false},
		{false, true, false, false, false},
		{false, false, false, false, false},
	})
	if metrics.HitAt1 != 1.0/3.0 || metrics.HitAt5 != 2.0/3.0 || metrics.MRRAt5 != 0.5 {
		t.Fatalf("metrics = %#v", metrics)
	}
}
