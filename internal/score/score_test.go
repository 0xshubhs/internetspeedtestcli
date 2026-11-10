package score

import "testing"

func TestBufferbloatGrade(t *testing.T) {
	cases := map[float64]string{0: "A+", 4.9: "A+", 5: "A", 29: "A", 30: "B", 59: "B", 60: "C", 199: "C", 200: "D", 399: "D", 400: "F", 2000: "F"}
	for in, want := range cases {
		if got := BufferbloatGrade(in); got != want {
			t.Errorf("BufferbloatGrade(%v) = %s, want %s", in, got, want)
		}
	}
}

func find(rs []Rating, name string) Rating {
	for _, r := range rs {
		if r.UseCase == name {
			return r
		}
	}
	return Rating{}
}

func TestRateGreatConnection(t *testing.T) {
	rs := Rate(Inputs{
		DownMbps: Of(500), UpMbps: Of(100), LatencyMs: Of(8),
		JitterMs: Of(1), LossPct: Of(0), BloatMs: Of(3),
	})
	for _, r := range rs {
		if r.Level != Great || r.Limit != "" {
			t.Errorf("%s = %v (limited by %q), want Great", r.UseCase, r.Level, r.Limit)
		}
	}
}

func TestRateReportsWorstMetric(t *testing.T) {
	rs := Rate(Inputs{
		DownMbps: Of(500), UpMbps: Of(100), LatencyMs: Of(15),
		JitterMs: Of(2), LossPct: Of(0), BloatMs: Of(250),
	})
	g := find(rs, "Gaming")
	if g.Level != Poor || g.Limit != "bufferbloat" {
		t.Fatalf("Gaming = %v limited by %q, want Poor limited by bufferbloat", g.Level, g.Limit)
	}
	if s := find(rs, "Streaming"); s.Level != Great {
		t.Fatalf("Streaming = %v, want Great", s.Level)
	}
}

func TestRateSkipsUseCasesMissingKeyMetrics(t *testing.T) {
	rs := Rate(Inputs{DownMbps: Of(2)})
	if len(rs) != 1 {
		t.Fatalf("got %d ratings, want only Streaming: %+v", len(rs), rs)
	}
	if s := find(rs, "Streaming"); s.Level != Poor || s.Limit != "download" {
		t.Fatalf("Streaming = %v limited by %q, want Poor limited by download", s.Level, s.Limit)
	}
	rs = Rate(Inputs{LatencyMs: Of(10), JitterMs: Of(1)})
	if len(rs) != 1 || find(rs, "Gaming").Level != Great {
		t.Fatalf("latency-only inputs should rate Gaming alone, got %+v", rs)
	}
	if rs := Rate(Inputs{}); len(rs) != 0 {
		t.Fatalf("empty inputs should produce no ratings, got %+v", rs)
	}
}
