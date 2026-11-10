package stats

import (
	"math"
	"sort"
	"time"
)

type Summary struct {
	Count  int     `json:"count"`
	Min    float64 `json:"min"`
	Mean   float64 `json:"mean"`
	Median float64 `json:"median"`
	P90    float64 `json:"p90"`
	Max    float64 `json:"max"`
	StdDev float64 `json:"stddev"`
	Jitter float64 `json:"jitter"`
}

func Summarize(samples []float64) Summary {
	n := len(samples)
	if n == 0 {
		return Summary{}
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)

	var sum float64
	for _, v := range samples {
		sum += v
	}
	mean := sum / float64(n)

	var sq float64
	for _, v := range samples {
		d := v - mean
		sq += d * d
	}

	var jitter float64
	if n > 1 {
		for i := 1; i < n; i++ {
			jitter += math.Abs(samples[i] - samples[i-1])
		}
		jitter /= float64(n - 1)
	}

	return Summary{
		Count:  n,
		Min:    sorted[0],
		Mean:   mean,
		Median: Percentile(sorted, 50),
		P90:    Percentile(sorted, 90),
		Max:    sorted[n-1],
		StdDev: math.Sqrt(sq / float64(n)),
		Jitter: jitter,
	}
}

func Percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	switch n {
	case 0:
		return 0
	case 1:
		return sorted[0]
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[n-1]
	}
	rank := p / 100 * float64(n-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(rank-float64(lo))
}

func Ms(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
