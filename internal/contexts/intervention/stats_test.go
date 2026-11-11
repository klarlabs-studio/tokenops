package intervention

import (
	"math"
	"testing"
)

func near(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

// The two-sided 95% points of Student's t, as every table prints them.
func TestTQuantileMatchesTheTables(t *testing.T) {
	for _, c := range []struct{ df, want float64 }{
		{1, 12.706}, {2, 4.303}, {4, 2.776}, {5, 2.571}, {10, 2.228},
		{7, 2.365}, {8, 2.306}, {30, 2.042}, {100, 1.984}, {1e6, 1.960},
	} {
		if got := tQuantile(0.975, c.df); !near(got, c.want, 0.002) {
			t.Errorf("t(0.975, %v) = %.4f, want %.3f", c.df, got, c.want)
		}
	}
}

// Wilson's interval and Newcombe's difference against their papers'
// worked examples.
func TestProportionIntervalsMatchThePublishedExamples(t *testing.T) {
	z := normalQuantile(0.975)
	if !near(z, 1.95996, 1e-4) {
		t.Fatalf("z = %v", z)
	}
	if lo, hi := wilson(0.5, 10, z); !near(lo, 0.2366, 1e-3) || !near(hi, 0.7634, 1e-3) {
		t.Errorf("Wilson 5/10 = %.4f–%.4f, want 0.2366–0.7634", lo, hi)
	}
	// Newcombe (1998), example (a): 56/70 against 48/80.
	in, ok := proportionDifference(56, 70, 48, 80)
	if !ok || !near(in.Low, 0.0524, 1e-3) || !near(in.High, 0.3339, 1e-3) {
		t.Errorf("Newcombe 56/70 − 48/80 = %.4f–%.4f, want 0.0524–0.3339", in.Low, in.High)
	}
	if _, ok := proportionDifference(1, 0, 1, 2); ok {
		t.Error("an interval over no executions")
	}
}

// Welch's interval: the difference, its standard error and degrees of
// freedom as computed independently (Python's statistics module) for
// these samples: 3.75, 1.01036, 7.2413.
func TestMeanDifferenceIsWelchs(t *testing.T) {
	a := []float64{12.0, 15.5, 9.0, 14.0, 11.5, 13.0}
	b := []float64{8.0, 9.5, 7.0, 10.5, 8.5, 9.0, 7.5, 10.0}
	diff, in, ok := meanDifference(a, b)
	if !ok || !near(diff, 3.75, 1e-9) {
		t.Fatalf("difference %v ok %v", diff, ok)
	}
	half := 1.0103629710818451 * tQuantile(0.975, 7.241275312365361)
	if !near(in.Low, 3.75-half, 1e-9) || !near(in.High, 3.75+half, 1e-9) || in.Level != 0.95 {
		t.Errorf("interval %+v, want 3.75 ± %.4f", in, half)
	}
	// Between the table's points for 7 and 8 degrees of freedom.
	if half < 1.0103629710818451*2.306 || half > 1.0103629710818451*2.365 {
		t.Errorf("half-width %.4f outside the t table's range for df 7.24", half)
	}
	if _, _, ok := meanDifference([]float64{1}, b); ok {
		t.Error("an interval from one value")
	}
	if d, in, ok := meanDifference([]float64{3, 3}, []float64{1, 1, 1}); !ok || d != 2 || in.Low != 2 || in.High != 2 {
		t.Errorf("constant samples: %v %+v %v", d, in, ok)
	}
}
