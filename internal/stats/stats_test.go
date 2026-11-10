package stats

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestSummarize(t *testing.T) {
	s := Summarize([]float64{10, 20, 30, 40})
	want := Summary{Count: 4, Min: 10, Mean: 25, Median: 25, P90: 37, Max: 40, StdDev: math.Sqrt(125), Jitter: 10}
	if s.Count != want.Count || !near(s.Min, want.Min) || !near(s.Mean, want.Mean) ||
		!near(s.Median, want.Median) || !near(s.P90, want.P90) || !near(s.Max, want.Max) ||
		!near(s.StdDev, want.StdDev) || !near(s.Jitter, want.Jitter) {
		t.Fatalf("Summarize = %+v, want %+v", s, want)
	}
}

func TestSummarizeKeepsArrivalOrderForJitter(t *testing.T) {
	s := Summarize([]float64{10, 30, 10, 30})
	if !near(s.Jitter, 20) {
		t.Fatalf("jitter = %v, want 20", s.Jitter)
	}
}

func TestSummarizeEmpty(t *testing.T) {
	if s := Summarize(nil); s != (Summary{}) {
		t.Fatalf("Summarize(nil) = %+v, want zero value", s)
	}
}

func TestPercentile(t *testing.T) {
	sorted := []float64{1, 2, 3, 4, 5}
	cases := map[float64]float64{0: 1, 50: 3, 100: 5, 25: 2, 90: 4.6}
	for p, want := range cases {
		if got := Percentile(sorted, p); !near(got, want) {
			t.Errorf("Percentile(%v) = %v, want %v", p, got, want)
		}
	}
	if got := Percentile([]float64{7}, 90); got != 7 {
		t.Errorf("single sample percentile = %v, want 7", got)
	}
}
