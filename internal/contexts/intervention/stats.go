package intervention

import "math"

// The tests below are the smallest that can tell a difference from noise.
// A verdict used to rest on five executions per side and a plain
// difference of means: with session spend varying by a factor of ten, a
// "saving" of that kind is as often noise as not.

// confidence is the two-sided level of every interval here.
const confidence = 0.95

// Interval is a range the true difference lies in at the stated
// confidence, in the unit of what was compared.
type Interval struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
	// Level is the confidence, 0.95 for a 95% interval.
	Level float64 `json:"level"`
}

// Excludes reports whether x lies outside the interval.
func (i Interval) Excludes(x float64) bool { return x < i.Low || x > i.High }

// meanDifference is the Welch interval for mean(a) − mean(b): two samples
// of unequal size and variance. ok is false when either side has fewer
// than two values, which leave no variance to test with.
func meanDifference(a, b []float64) (diff float64, in Interval, ok bool) {
	if len(a) < 2 || len(b) < 2 {
		return 0, Interval{}, false
	}
	ma, va := meanVar(a)
	mb, vb := meanVar(b)
	na, nb := float64(len(a)), float64(len(b))
	sa, sb := va/na, vb/nb
	diff = ma - mb
	se := math.Sqrt(sa + sb)
	if se == 0 {
		// Every value on each side equal: the difference is exact.
		return diff, Interval{Low: diff, High: diff, Level: confidence}, true
	}
	// Welch–Satterthwaite degrees of freedom.
	df := (sa + sb) * (sa + sb) / (sa*sa/(na-1) + sb*sb/(nb-1))
	t := tQuantile(1-(1-confidence)/2, df)
	return diff, Interval{Low: diff - t*se, High: diff + t*se, Level: confidence}, true
}

// meanVar is the mean and the unbiased sample variance of xs (len ≥ 2).
func meanVar(xs []float64) (mean, variance float64) {
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	for _, x := range xs {
		d := x - mean
		variance += d * d
	}
	return mean, variance / float64(len(xs)-1)
}

// proportionDifference is Newcombe's hybrid score interval for p1 − p2,
// p1 = s1/n1 and p2 = s2/n2, built from each proportion's Wilson interval.
// It holds its level at small counts and at rates near 0 or 1, where the
// plain normal interval does not. Successes may be fractional (a partial
// outcome counts half).
func proportionDifference(s1, n1, s2, n2 float64) (Interval, bool) {
	if n1 <= 0 || n2 <= 0 {
		return Interval{}, false
	}
	z := normalQuantile(1 - (1-confidence)/2)
	p1, p2 := s1/n1, s2/n2
	l1, u1 := wilson(p1, n1, z)
	l2, u2 := wilson(p2, n2, z)
	d := p1 - p2
	return Interval{
		Low:   d - math.Sqrt((p1-l1)*(p1-l1)+(u2-p2)*(u2-p2)),
		High:  d + math.Sqrt((u1-p1)*(u1-p1)+(p2-l2)*(p2-l2)),
		Level: confidence,
	}, true
}

// wilson is the Wilson score interval of a proportion p over n.
func wilson(p, n, z float64) (low, high float64) {
	z2 := z * z
	centre := (p + z2/(2*n)) / (1 + z2/n)
	half := z / (1 + z2/n) * math.Sqrt(p*(1-p)/n+z2/(4*n*n))
	return centre - half, centre + half
}

// normalQuantile is the standard normal's p-quantile.
func normalQuantile(p float64) float64 { return math.Sqrt2 * math.Erfinv(2*p-1) }

// tQuantile is Student's t p-quantile with df degrees of freedom (p > 0.5),
// found by bisection on the distribution function.
func tQuantile(p, df float64) float64 {
	lo, hi := 0.0, 1.0
	for tCDF(hi, df) < p {
		hi *= 2
	}
	for range 100 {
		mid := (lo + hi) / 2
		if tCDF(mid, df) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// tCDF is Student's t distribution function at t ≥ 0.
func tCDF(t, df float64) float64 {
	x := df / (df + t*t)
	return 1 - 0.5*regIncBeta(df/2, 0.5, x)
}

// regIncBeta is the regularized incomplete beta function I_x(a, b), by
// its continued fraction (Lentz's method).
func regIncBeta(a, b, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	front := math.Exp(lab - la - lb + a*math.Log(x) + b*math.Log(1-x))
	// The fraction converges fast for x below its mean; above it, use
	// the symmetry I_x(a, b) = 1 − I_{1−x}(b, a).
	if x > (a+1)/(a+b+2) {
		return 1 - regIncBeta(b, a, 1-x)
	}
	const tiny = 1e-300
	f, c, d := 1.0, 1.0, 0.0
	for i := 0; i <= 300; i++ {
		m := float64(i / 2)
		var num float64
		switch {
		case i == 0:
			num = 1
		case i%2 == 0:
			num = m * (b - m) * x / ((a + 2*m - 1) * (a + 2*m))
		default:
			num = -(a + m) * (a + b + m) * x / ((a + 2*m) * (a + 2*m + 1))
		}
		d = 1 + num*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		d = 1 / d
		c = 1 + num/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		cd := c * d
		f *= cd
		if math.Abs(1-cd) < 1e-12 {
			break
		}
	}
	return front * (f - 1) / a
}
