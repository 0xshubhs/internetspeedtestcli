package throughput

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type Direction int

const (
	Download Direction = iota
	Upload
)

func (d Direction) String() string {
	if d == Upload {
		return "upload"
	}
	return "download"
}

type Config struct {
	Direction  Direction
	DownURL    func(bytes int64) string
	UpURL      string
	Streams    int
	Duration   time.Duration
	Warmup     time.Duration
	ChunkBytes int64
	Transport  func() *http.Transport
	UserAgent  string
	OnTick     func(Tick)
}

type Tick struct {
	Elapsed  time.Duration
	Bytes    int64
	Bps      float64
	Fraction float64
}

type Result struct {
	Bps     float64
	PeakBps float64
	Bytes   int64
	Elapsed time.Duration
	Streams int
	Errors  int
	Series  []float64
	LastErr error
}

type point struct {
	at    time.Duration
	bytes int64
}

const tickEvery = 100 * time.Millisecond

var payload = func() []byte {
	b := make([]byte, 1<<20)
	_, _ = rand.Read(b)
	return b
}()

func Run(ctx context.Context, cfg Config) (Result, error) {
	if cfg.Streams < 1 {
		cfg.Streams = 1
	}
	if cfg.ChunkBytes <= 0 {
		cfg.ChunkBytes = 25_000_000
	}
	if cfg.Warmup >= cfg.Duration {
		cfg.Warmup = cfg.Duration / 4
	}

	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var total atomic.Int64
	var errCount atomic.Int64
	var lastErr atomic.Pointer[error]
	fatal := make(chan error, 1)

	transports := make([]*http.Transport, cfg.Streams)
	var wg sync.WaitGroup
	start := time.Now()
	for i := range cfg.Streams {
		tr := cfg.Transport()
		transports[i] = tr
		client := &http.Client{Transport: tr}
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := worker{cfg: cfg, client: client, total: &total}
			for ctx.Err() == nil {
				err := w.once(ctx)
				if err == nil || ctx.Err() != nil {
					continue
				}
				errCount.Add(1)
				lastErr.Store(&err)
				var se *StatusError
				if errors.As(err, &se) && se.Fatal() {
					select {
					case fatal <- err:
					default:
					}
					return
				}
				select {
				case <-ctx.Done():
				case <-time.After(250 * time.Millisecond):
				}
			}
		}()
	}

	points := []point{{0, 0}}
	ticker := time.NewTicker(tickEvery)
	deadline := time.NewTimer(cfg.Duration)
	defer ticker.Stop()
	defer deadline.Stop()

	var abortErr error
loop:
	for {
		select {
		case <-parent.Done():
			abortErr = parent.Err()
			break loop
		case <-deadline.C:
			break loop
		case err := <-fatal:
			abortErr = err
			break loop
		case now := <-ticker.C:
			el := now.Sub(start)
			b := total.Load()
			points = append(points, point{el, b})
			if cfg.OnTick != nil {
				cfg.OnTick(Tick{
					Elapsed:  el,
					Bytes:    b,
					Bps:      windowRate(points, len(points)-1, time.Second),
					Fraction: min(1, float64(el)/float64(cfg.Duration)),
				})
			}
			if b == 0 && el > 4*time.Second && errCount.Load() >= int64(cfg.Streams) {
				abortErr = errors.New("no data transferred")
				if p := lastErr.Load(); p != nil {
					abortErr = *p
				}
				break loop
			}
		}
	}

	end := time.Since(start)
	endBytes := total.Load()
	points = append(points, point{end, endBytes})
	cancel()
	wg.Wait()
	for _, tr := range transports {
		tr.CloseIdleConnections()
	}

	res := Result{
		Bytes:   endBytes,
		Elapsed: end,
		Streams: cfg.Streams,
		Errors:  int(errCount.Load()),
	}
	if p := lastErr.Load(); p != nil {
		res.LastErr = *p
	}
	if abortErr != nil {
		return res, abortErr
	}
	if endBytes == 0 {
		if res.LastErr != nil {
			return res, res.LastErr
		}
		return res, errors.New("no data transferred")
	}

	from := points[0]
	for _, p := range points {
		if p.at >= cfg.Warmup {
			from = p
			break
		}
	}
	if span := end - from.at; span > 0 {
		res.Bps = float64(endBytes-from.bytes) * 8 / span.Seconds()
	}
	for i := range points {
		if points[i].at < time.Second {
			continue
		}
		res.PeakBps = max(res.PeakBps, windowRate(points, i, time.Second))
	}
	res.PeakBps = max(res.PeakBps, res.Bps)
	for i := 1; i < len(points); i++ {
		res.Series = append(res.Series, windowRate(points, i, 500*time.Millisecond))
	}
	return res, nil
}

func windowRate(points []point, i int, window time.Duration) float64 {
	if i <= 0 {
		return 0
	}
	cur := points[i]
	j := i - 1
	for j > 0 && cur.at-points[j].at < window {
		j--
	}
	dt := cur.at - points[j].at
	if dt <= 0 {
		return 0
	}
	return float64(cur.bytes-points[j].bytes) * 8 / dt.Seconds()
}

type StatusError struct {
	Code   int
	Status string
}

func (e *StatusError) Error() string {
	if e.Code == http.StatusTooManyRequests {
		return "server is rate limiting this IP (" + e.Status + "), wait a few minutes or test against your own server with --server"
	}
	return "server answered " + e.Status
}

func (e *StatusError) Fatal() bool {
	return e.Code == http.StatusTooManyRequests || e.Code == http.StatusForbidden ||
		e.Code == http.StatusNotFound || e.Code == http.StatusMethodNotAllowed ||
		e.Code == http.StatusRequestEntityTooLarge || e.Code == http.StatusBadRequest
}

type worker struct {
	cfg    Config
	client *http.Client
	total  *atomic.Int64
	buf    []byte
}

func (w *worker) once(ctx context.Context) error {
	if w.cfg.Direction == Upload {
		return w.upload(ctx)
	}
	return w.download(ctx)
}

func (w *worker) download(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.cfg.DownURL(w.cfg.ChunkBytes), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", w.cfg.UserAgent)
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return &StatusError{Code: resp.StatusCode, Status: resp.Status}
	}
	if w.buf == nil {
		w.buf = make([]byte, 256<<10)
	}
	for {
		n, err := resp.Body.Read(w.buf)
		w.total.Add(int64(n))
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (w *worker) upload(ctx context.Context) error {
	body := &countingReader{remaining: w.cfg.ChunkBytes, total: w.total}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.cfg.UpURL, body)
	if err != nil {
		return err
	}
	req.ContentLength = w.cfg.ChunkBytes
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", w.cfg.UserAgent)
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return &StatusError{Code: resp.StatusCode, Status: resp.Status}
	}
	return nil
}

type countingReader struct {
	remaining int64
	off       int
	total     *atomic.Int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	written := 0
	for written < n {
		c := copy(p[written:n], payload[r.off:])
		written += c
		r.off = (r.off + c) % len(payload)
	}
	r.remaining -= int64(n)
	r.total.Add(int64(n))
	return n, nil
}
