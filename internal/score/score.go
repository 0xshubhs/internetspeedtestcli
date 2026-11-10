package score

type Level int

const (
	Unknown Level = iota
	Poor
	Fair
	Good
	Great
)

func (l Level) String() string {
	switch l {
	case Poor:
		return "Poor"
	case Fair:
		return "Fair"
	case Good:
		return "Good"
	case Great:
		return "Great"
	}
	return "n/a"
}

func (l Level) MarshalText() ([]byte, error) {
	return []byte(l.String()), nil
}

func BufferbloatGrade(increaseMs float64) string {
	switch {
	case increaseMs < 5:
		return "A+"
	case increaseMs < 30:
		return "A"
	case increaseMs < 60:
		return "B"
	case increaseMs < 200:
		return "C"
	case increaseMs < 400:
		return "D"
	}
	return "F"
}

type Metric struct {
	Value float64
	Known bool
}

func Of(v float64) Metric { return Metric{Value: v, Known: true} }

type Inputs struct {
	DownMbps  Metric
	UpMbps    Metric
	LatencyMs Metric
	JitterMs  Metric
	LossPct   Metric
	BloatMs   Metric
}

type Rating struct {
	UseCase string `json:"use_case"`
	Level   Level  `json:"rating"`
	Limit   string `json:"limited_by,omitempty"`
}

type rule struct {
	name              string
	m                 Metric
	higherIsBetter    bool
	great, good, fair float64
}

func (r rule) level() Level {
	v := r.m.Value
	if r.higherIsBetter {
		switch {
		case v >= r.great:
			return Great
		case v >= r.good:
			return Good
		case v >= r.fair:
			return Fair
		}
		return Poor
	}
	switch {
	case v <= r.great:
		return Great
	case v <= r.good:
		return Good
	case v <= r.fair:
		return Fair
	}
	return Poor
}

func evaluate(name string, required []Metric, rules ...rule) Rating {
	out := Rating{UseCase: name}
	for _, m := range required {
		if !m.Known {
			return out
		}
	}
	for _, r := range rules {
		if !r.m.Known {
			continue
		}
		l := r.level()
		if out.Level == Unknown || l < out.Level {
			out.Level = l
			out.Limit = ""
			if l < Great {
				out.Limit = r.name
			}
		}
	}
	return out
}

func Rate(in Inputs) []Rating {
	all := []Rating{
		evaluate("Streaming", []Metric{in.DownMbps},
			rule{"download", in.DownMbps, true, 25, 15, 5},
			rule{"latency", in.LatencyMs, false, 100, 200, 500},
			rule{"packet loss", in.LossPct, false, 1, 3, 5},
		),
		evaluate("Gaming", []Metric{in.LatencyMs},
			rule{"latency", in.LatencyMs, false, 20, 50, 100},
			rule{"jitter", in.JitterMs, false, 5, 15, 30},
			rule{"packet loss", in.LossPct, false, 0.5, 1.5, 3},
			rule{"bufferbloat", in.BloatMs, false, 30, 60, 200},
			rule{"download", in.DownMbps, true, 10, 3, 1},
			rule{"upload", in.UpMbps, true, 3, 1, 0.5},
		),
		evaluate("Video calls", []Metric{in.UpMbps, in.LatencyMs},
			rule{"upload", in.UpMbps, true, 5, 2, 1},
			rule{"download", in.DownMbps, true, 5, 2, 1},
			rule{"latency", in.LatencyMs, false, 50, 100, 200},
			rule{"jitter", in.JitterMs, false, 10, 20, 40},
			rule{"packet loss", in.LossPct, false, 1, 2, 5},
			rule{"bufferbloat", in.BloatMs, false, 60, 200, 400},
		),
	}
	var out []Rating
	for _, r := range all {
		if r.Level != Unknown {
			out = append(out, r)
		}
	}
	return out
}
