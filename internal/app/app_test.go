package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/0xshubhs/internetspeedtestcli/internal/server"
)

func TestParseServer(t *testing.T) {
	cases := map[string]string{
		"":                        "https://speed.cloudflare.com",
		"speed.cloudflare.com":    "https://speed.cloudflare.com",
		"example.com:443":         "https://example.com:443",
		"192.168.1.10:8080":       "http://192.168.1.10:8080",
		"[::1]:8080":              "http://[::1]:8080",
		"http://host:9000/speed/": "http://host:9000/speed",
		"https://host/x?y=1#z":    "https://host/x",
	}
	for in, want := range cases {
		u, err := parseServer(in)
		if err != nil {
			t.Errorf("parseServer(%q) error: %v", in, err)
			continue
		}
		if u.String() != want {
			t.Errorf("parseServer(%q) = %s, want %s", in, u, want)
		}
	}
	for _, bad := range []string{"ftp://host", "http://"} {
		if _, err := parseServer(bad); err == nil {
			t.Errorf("parseServer(%q) should fail", bad)
		}
	}
}

func TestParseFlags(t *testing.T) {
	cfg, _, exit := parseFlags([]string{"-d", "5s", "--connections", "4", "-4", "--loss-method", "DNS", "--json"})
	if exit {
		t.Fatal("valid flags rejected")
	}
	if cfg.Duration != 5*time.Second || cfg.Connections != 4 || cfg.IPVersion() != 4 || cfg.LossMethod != "udp" || cfg.Pretty() {
		t.Fatalf("unexpected config %+v", cfg)
	}
	for _, bad := range [][]string{
		{"-4", "-6"},
		{"-d", "1s"},
		{"-c", "0"},
		{"--json", "--csv"},
		{"--loss-method", "carrier-pigeon"},
		{"stray"},
		{"--no-download", "--no-upload", "--no-latency", "--no-loss"},
	} {
		if _, code, exit := parseFlags(bad); !exit || code != 2 {
			t.Errorf("parseFlags(%v) = exit %v code %d, want usage error", bad, exit, code)
		}
	}
}

func TestFormatting(t *testing.T) {
	if got := fmtRate(93_410_000, false); got != "93.41 Mbps" {
		t.Errorf("fmtRate = %q", got)
	}
	if got := fmtRate(1_250_000_000, false); got != "1.25 Gbps" {
		t.Errorf("fmtRate = %q", got)
	}
	if got := fmtRate(80_000_000, true); got != "10.00 MB/s" {
		t.Errorf("fmtRate bytes = %q", got)
	}
	if got := fmtMs(2.345); got != "2.35 ms" {
		t.Errorf("fmtMs = %q", got)
	}
	if got := fmtMs(123.4); got != "123 ms" {
		t.Errorf("fmtMs = %q", got)
	}
	if got := fmtBytes(117_400_000); got != "117.4 MB" {
		t.Errorf("fmtBytes = %q", got)
	}
}

func TestFullRunAgainstLocalServer(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	pc, err := net.ListenPacket("udp4", ln.Addr().String())
	if err != nil {
		t.Skipf("cannot open UDP: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, port, _ := net.SplitHostPort(pc.LocalAddr().String())
	srv := &http.Server{Handler: server.Handler(server.Options{Version: "test", Name: "local"}, port)}
	go srv.Serve(ln)
	defer srv.Close()
	go server.ServeUDPEcho(ctx, pc)

	cfg, _, exit := parseFlags([]string{
		"--server", "http://" + ln.Addr().String(),
		"-d", "2s", "-c", "2", "--chunk-mb", "2",
		"--latency-count", "5", "--loss-count", "20", "--loss-interval", "2ms",
		"--json",
	})
	if exit {
		t.Fatal("flags rejected")
	}
	a, err := newApp(cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.run(ctx); err != nil {
		t.Fatal(err)
	}
	r := a.rep
	if len(r.Errors) > 0 {
		t.Fatalf("errors: %v", r.Errors)
	}
	if r.Server.Provider != "netspeed server" || r.Server.Location != "local" || r.Client.IP != "127.0.0.1" {
		t.Fatalf("server/client info %+v %+v", r.Server, r.Client)
	}
	if r.Latency == nil || r.Latency.Stats.Count != 5 {
		t.Fatalf("latency %+v", r.Latency)
	}
	if r.PacketLoss == nil || r.PacketLoss.Method != "udp-echo" || r.PacketLoss.Sent != 20 {
		t.Fatalf("packet loss %+v", r.PacketLoss)
	}
	for name, tr := range map[string]*TransferInfo{"download": r.Download, "upload": r.Upload} {
		if tr == nil || tr.Mbps <= 0 || tr.LoadedLatency == nil || tr.LatencyIncreaseMs == nil || tr.LoadedLoss == nil {
			t.Fatalf("%s info incomplete: %+v", name, tr)
		}
	}
	if r.Bufferbloat == nil || r.Bufferbloat.Grade == "" || len(r.Quality) != 3 {
		t.Fatalf("summary missing: %+v %+v", r.Bufferbloat, r.Quality)
	}

	b, err := json.Marshal(r.rounded())
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"server", "client", "connection", "latency", "packet_loss", "download", "upload", "bufferbloat", "quality"} {
		if _, ok := back[k]; !ok {
			t.Errorf("JSON report missing %q", k)
		}
	}

	var buf bytes.Buffer
	a.cfg.CSVHeader = true
	a.writeCSV(&buf)
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil || len(rows) != 2 || len(rows[0]) != len(rows[1]) {
		t.Fatalf("CSV output malformed: %v %v", rows, err)
	}
}
