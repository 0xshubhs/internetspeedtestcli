package loss

import (
	"context"
	"sync"
	"time"

	"github.com/0xshubhs/internetspeedtestcli/internal/stats"
)

type Conn interface {
	Send(seq uint16) error
	Recv(buf []byte) (seq uint16, ok bool, err error)
	Close() error
	Method() string
	Target() string
}

type Result struct {
	Method     string        `json:"method"`
	Target     string        `json:"target"`
	Sent       int           `json:"sent"`
	Received   int           `json:"received"`
	Lost       int           `json:"lost"`
	Duplicates int           `json:"duplicates"`
	LossPct    float64       `json:"loss_pct"`
	RTT        stats.Summary `json:"rtt_ms"`
}

type Progress struct {
	Sent     int
	Received int
}

type Session struct {
	c        Conn
	interval time.Duration
	wait     time.Duration
	limit    int

	mu      sync.Mutex
	sendAt  []time.Time
	got     []bool
	rtts    []float64
	recvd   int
	dups    int
	sending bool

	stopSend chan struct{}
	stopOnce sync.Once
	result   chan Result
	onProg   func(Progress)
}

func Start(ctx context.Context, c Conn, count int, interval, wait time.Duration, onProgress func(Progress)) *Session {
	s := &Session{
		c:        c,
		interval: interval,
		wait:     wait,
		limit:    count,
		sending:  true,
		stopSend: make(chan struct{}),
		result:   make(chan Result, 1),
		onProg:   onProgress,
	}
	recvDone := make(chan struct{})
	go s.recvLoop(recvDone)
	go func() {
		s.sendLoop(ctx)
		s.mu.Lock()
		s.sending = false
		s.mu.Unlock()
		s.drain(ctx)
		s.c.Close()
		<-recvDone
		s.result <- s.snapshot()
	}()
	return s
}

func Run(ctx context.Context, c Conn, count int, interval, wait time.Duration, onProgress func(Progress)) Result {
	return Start(ctx, c, count, interval, wait, onProgress).Wait()
}

func (s *Session) Stop() Result {
	s.stopOnce.Do(func() { close(s.stopSend) })
	return s.Wait()
}

func (s *Session) Wait() Result {
	r := <-s.result
	s.result <- r
	return r
}

func (s *Session) sendLoop(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for i := 0; s.limit <= 0 || i < s.limit; i++ {
		s.mu.Lock()
		seq := uint16(len(s.sendAt))
		s.sendAt = append(s.sendAt, time.Now())
		s.got = append(s.got, false)
		s.mu.Unlock()
		_ = s.c.Send(seq)
		s.progress()
		if s.limit > 0 && i == s.limit-1 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-s.stopSend:
			return
		case <-ticker.C:
		}
	}
}

func (s *Session) drain(ctx context.Context) {
	deadline := time.Now().Add(s.wait)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		done := s.recvd >= len(s.sendAt)
		s.mu.Unlock()
		if done {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *Session) recvLoop(done chan struct{}) {
	defer close(done)
	buf := make([]byte, 1500)
	for {
		seq, ok, err := s.c.Recv(buf)
		if err != nil {
			return
		}
		if !ok {
			continue
		}
		now := time.Now()
		s.mu.Lock()
		idx := resolveIndex(len(s.sendAt), seq)
		if idx >= 0 {
			if s.got[idx] {
				s.dups++
			} else {
				s.got[idx] = true
				s.recvd++
				s.rtts = append(s.rtts, stats.Ms(now.Sub(s.sendAt[idx])))
			}
		}
		s.mu.Unlock()
		if idx >= 0 {
			s.progress()
		}
	}
}

func resolveIndex(n int, seq uint16) int {
	if n == 0 {
		return -1
	}
	last := n - 1
	cand := (last &^ 0xFFFF) | int(seq)
	if cand > last {
		cand -= 0x10000
	}
	if cand < 0 {
		return -1
	}
	return cand
}

func (s *Session) progress() {
	if s.onProg == nil {
		return
	}
	s.mu.Lock()
	p := Progress{Sent: len(s.sendAt), Received: s.recvd}
	s.mu.Unlock()
	s.onProg(p)
}

func (s *Session) snapshot() Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := Result{
		Method:     s.c.Method(),
		Target:     s.c.Target(),
		Sent:       len(s.sendAt),
		Received:   s.recvd,
		Duplicates: s.dups,
		RTT:        stats.Summarize(s.rtts),
	}
	r.Lost = r.Sent - r.Received
	if r.Sent > 0 {
		r.LossPct = float64(r.Lost) / float64(r.Sent) * 100
	}
	return r
}
