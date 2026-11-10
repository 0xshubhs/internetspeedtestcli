package probe

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/0xshubhs/internetspeedtestcli/internal/stats"
)

var ErrNoSamples = errors.New("no latency samples could be collected")

type Pinger interface {
	Ping(ctx context.Context) (time.Duration, error)
}

type TCPPinger struct {
	Addr    string
	Network string
	Timeout time.Duration
}

func (p TCPPinger) Ping(ctx context.Context) (time.Duration, error) {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	start := time.Now()
	c, err := d.DialContext(ctx, p.Network, p.Addr)
	rtt := time.Since(start)
	if err != nil {
		return 0, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	c.Close()
	return rtt, nil
}

func Collect(ctx context.Context, p Pinger, count int, interval time.Duration, onSample func(done int, rtt float64)) ([]float64, int, error) {
	samples := make([]float64, 0, count)
	failed := 0
	for i := 0; i < count; i++ {
		start := time.Now()
		rtt, err := p.Ping(ctx)
		if ctx.Err() != nil {
			return samples, failed, ctx.Err()
		}
		if err != nil {
			failed++
		} else {
			ms := stats.Ms(rtt)
			samples = append(samples, ms)
			if onSample != nil {
				onSample(i+1, ms)
			}
		}
		if i < count-1 {
			if wait := interval - time.Since(start); wait > 0 {
				select {
				case <-ctx.Done():
					return samples, failed, ctx.Err()
				case <-time.After(wait):
				}
			}
		}
	}
	if len(samples) == 0 {
		return samples, failed, ErrNoSamples
	}
	return samples, failed, nil
}

type Sample struct {
	At  time.Time
	RTT float64
}

type Monitor struct {
	mu      sync.Mutex
	samples []Sample
	failed  int
	last    float64
	cancel  context.CancelFunc
	done    chan struct{}
}

func StartMonitor(ctx context.Context, p Pinger, interval time.Duration) *Monitor {
	ctx, cancel := context.WithCancel(ctx)
	m := &Monitor{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			rtt, err := p.Ping(ctx)
			if ctx.Err() != nil {
				return
			}
			m.mu.Lock()
			if err != nil {
				m.failed++
			} else {
				ms := stats.Ms(rtt)
				m.samples = append(m.samples, Sample{At: time.Now(), RTT: ms})
				m.last = ms
			}
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return m
}

func (m *Monitor) Last() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

func (m *Monitor) Stop() ([]Sample, int) {
	m.cancel()
	<-m.done
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.samples, m.failed
}
