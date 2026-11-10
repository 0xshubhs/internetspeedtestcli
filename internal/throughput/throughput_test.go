package throughput_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0xshubhs/internetspeedtestcli/internal/server"
	"github.com/0xshubhs/internetspeedtestcli/internal/throughput"
)

func config(base string, dir throughput.Direction) throughput.Config {
	return throughput.Config{
		Direction:  dir,
		DownURL:    func(n int64) string { return base + "/__down?bytes=" + strconv.FormatInt(n, 10) },
		UpURL:      base + "/__up",
		Streams:    3,
		Duration:   2 * time.Second,
		Warmup:     500 * time.Millisecond,
		ChunkBytes: 2_000_000,
		Transport:  func() *http.Transport { return &http.Transport{DisableCompression: true} },
		UserAgent:  "netspeed-test",
	}
}

func TestDownloadAndUpload(t *testing.T) {
	srv := httptest.NewServer(server.Handler(server.Options{Version: "test", Name: "test"}, ""))
	defer srv.Close()

	for _, dir := range []throughput.Direction{throughput.Download, throughput.Upload} {
		t.Run(dir.String(), func(t *testing.T) {
			ticks := 0
			cfg := config(srv.URL, dir)
			cfg.OnTick = func(throughput.Tick) { ticks++ }
			res, err := throughput.Run(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if res.Bytes < 2_000_000 || res.Bps <= 0 || res.PeakBps < res.Bps {
				t.Fatalf("implausible result %+v", res)
			}
			if res.Streams != 3 || len(res.Series) == 0 || ticks == 0 {
				t.Fatalf("missing details: streams %d, series %d, ticks %d", res.Streams, len(res.Series), ticks)
			}
			if res.Elapsed < 2*time.Second || res.Elapsed > 3*time.Second {
				t.Fatalf("elapsed %v, want about 2s", res.Elapsed)
			}
		})
	}
}

func TestRateLimitAbortsImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	start := time.Now()
	_, err := throughput.Run(context.Background(), config(srv.URL, throughput.Download))
	if err == nil || !strings.Contains(err.Error(), "rate limiting") {
		t.Fatalf("error = %v, want a rate limiting error", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("took %v to give up, want an immediate abort", time.Since(start))
	}
}

func TestCancelStopsTransfer(t *testing.T) {
	srv := httptest.NewServer(server.Handler(server.Options{Version: "test"}, ""))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := throughput.Run(ctx, config(srv.URL, throughput.Download))
	if err == nil {
		t.Fatal("expected an error after cancellation")
	}
}
