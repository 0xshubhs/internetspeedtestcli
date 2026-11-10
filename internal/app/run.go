package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/0xshubhs/internetspeedtestcli/internal/loss"
	"github.com/0xshubhs/internetspeedtestcli/internal/probe"
	"github.com/0xshubhs/internetspeedtestcli/internal/score"
	"github.com/0xshubhs/internetspeedtestcli/internal/server"
	"github.com/0xshubhs/internetspeedtestcli/internal/stats"
	"github.com/0xshubhs/internetspeedtestcli/internal/throughput"
	"github.com/0xshubhs/internetspeedtestcli/internal/ui"
)

type App struct {
	cfg     Config
	ui      *ui.Printer
	pretty  bool
	version string
	ua      string

	base     *url.URL
	host     string
	remoteIP net.IP
	pinned   string
	network  string
	udpPort  string

	pinger   probe.Pinger
	lossOpen loss.Opener
	rep      *Report
}

func Main(args []string, version string) int {
	if len(args) > 0 {
		switch args[0] {
		case "serve", "server":
			return server.Main(args[1:], version)
		case "version":
			fmt.Println("netspeed", version)
			return 0
		case "help":
			fmt.Print(usageText)
			return 0
		}
	}

	cfg, code, exit := parseFlags(args)
	if exit {
		return code
	}
	if cfg.Version {
		fmt.Println("netspeed", version)
		return 0
	}

	a, err := newApp(cfg, version)
	if err != nil {
		return usageError(err.Error())
	}
	if a.pretty {
		a.ui = ui.New(os.Stdout, cfg.NoColor)
	} else if p := ui.New(os.Stderr, cfg.NoColor); p.TTY() {
		a.ui = p
	} else {
		a.ui = ui.Discard()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a.ui.HideCursor()
	runErr := a.run(ctx)
	a.ui.Abort()

	if ctx.Err() != nil {
		fmt.Fprintln(os.Stderr, "\nnetspeed: interrupted")
		return 130
	}
	if runErr != nil {
		a.rep.Errors = append(a.rep.Errors, runErr.Error())
		if a.pretty || cfg.Simple {
			fmt.Fprintf(os.Stderr, "netspeed: %v\n", runErr)
			return 1
		}
	}

	switch {
	case cfg.JSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(a.rep.rounded())
	case cfg.CSV:
		a.writeCSV(os.Stdout)
	case cfg.Simple:
		a.writeSimple(os.Stdout)
	}
	if len(a.rep.Errors) > 0 {
		return 1
	}
	return 0
}

func newApp(cfg Config, version string) (*App, error) {
	base, err := parseServer(cfg.Server)
	if err != nil {
		return nil, err
	}
	return &App{
		cfg:     cfg,
		ui:      ui.Discard(),
		pretty:  cfg.Pretty(),
		version: version,
		ua:      "netspeed/" + version,
		base:    base,
		host:    base.Hostname(),
		rep: &Report{
			Timestamp: time.Now().UTC(),
			Version:   version,
			Server:    ServerInfo{URL: base.String(), Host: base.Hostname()},
		},
	}, nil
}

func (a *App) endpoint(path string) *url.URL {
	u := *a.base
	u.Path = a.base.Path + path
	return &u
}

func (a *App) downURL(n int64) string {
	u := a.endpoint("/__down")
	u.RawQuery = "bytes=" + strconv.FormatInt(n, 10)
	return u.String()
}

func (a *App) upURL() string {
	return a.endpoint("/__up").String()
}

func (a *App) newTransport() *http.Transport {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	network, addr := a.network, a.pinned
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, addr)
		},
		TLSClientConfig:       &tls.Config{ServerName: a.host, NextProtos: []string{"http/1.1"}},
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		ForceAttemptHTTP2:     false,
		DisableCompression:    true,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		WriteBufferSize:       256 << 10,
		ReadBufferSize:        256 << 10,
	}
}

func (a *App) run(ctx context.Context) error {
	live := a.ui.Live(a.label("Connecting"))
	live.Set(a.ui.Dim(a.base.Host))
	setup, err := probe.MeasureSetup(ctx, probe.SetupConfig{
		URL:        a.base,
		ProbeURL:   a.downURL(0),
		IPVersion:  a.cfg.IPVersion(),
		UserAgent:  a.ua,
		WarmProbes: 5,
	})
	live.Finish()
	if err != nil {
		return err
	}
	a.applySetup(setup)
	a.printHeader()

	steps := []struct {
		skip bool
		name string
		run  func(context.Context) error
	}{
		{a.cfg.NoLatency, "latency", a.latencyPhase},
		{a.cfg.NoLoss, "packet loss", a.lossPhase},
		{a.cfg.NoDownload, "download", func(ctx context.Context) error { return a.transferPhase(ctx, throughput.Download) }},
		{a.cfg.NoUpload, "upload", func(ctx context.Context) error { return a.transferPhase(ctx, throughput.Upload) }},
	}
	for _, s := range steps {
		if s.skip {
			continue
		}
		if err := s.run(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			a.rep.Errors = append(a.rep.Errors, fmt.Sprintf("%s: %v", s.name, err))
		}
	}

	a.finalize()
	a.printSummary()
	return nil
}

func (a *App) applySetup(s *probe.Setup) {
	h := s.Header
	a.remoteIP = s.RemoteIP
	a.pinned = net.JoinHostPort(s.RemoteIP.String(), s.Port)
	a.network = "tcp4"
	ipv := 4
	if s.RemoteIP.To4() == nil {
		a.network = "tcp6"
		ipv = 6
	}
	a.udpPort = h.Get("x-netspeed-udp")
	a.pinger = probe.TCPPinger{Addr: a.pinned, Network: a.network, Timeout: 2 * time.Second}

	provider := "Custom server"
	switch {
	case h.Get("x-netspeed-server") != "":
		provider = "netspeed server"
	case a.host == "speed.cloudflare.com":
		provider = "Cloudflare"
	}
	location := h.Get("colo")
	if location == "" {
		location = h.Get("cf-meta-colo")
	}
	a.rep.Server.IP = s.RemoteIP.String()
	a.rep.Server.Provider = provider
	a.rep.Server.Location = location

	city, err := url.QueryUnescape(h.Get("city"))
	if err != nil {
		city = h.Get("city")
	}
	asn := h.Get("asn")
	if asn != "" {
		asn = "AS" + asn
	}
	a.rep.Client = ClientInfo{
		IP:        h.Get("cf-meta-ip"),
		ASN:       asn,
		City:      city,
		Country:   h.Get("country"),
		IPVersion: ipv,
	}
	if s.LocalIP != nil {
		a.rep.Client.LocalIP = s.LocalIP.String()
	}

	ci := &ConnectionInfo{
		DNSMs:      stats.Ms(s.DNS),
		TCPMs:      stats.Ms(s.TCP),
		TLSMs:      stats.Ms(s.TLS),
		TTFBMs:     stats.Ms(s.TTFB),
		Protocol:   s.Protocol,
		TLSVersion: s.TLSVersion,
		Cipher:     s.Cipher,
	}
	if len(s.HTTPRTT) > 0 {
		ci.HTTPRTTMs = stats.Summarize(s.HTTPRTT).Median
	}
	a.rep.Connection = ci
}

func (a *App) latencyPhase(ctx context.Context) error {
	live := a.ui.Live(a.label("Latency"))
	n := a.cfg.LatencyCount
	samples, failed, err := probe.Collect(ctx, a.pinger, n, 60*time.Millisecond, func(done int, rtt float64) {
		live.Set(a.ui.Bold(fmtMs(rtt)) + "  " + a.ui.Dim(fmt.Sprintf("%d/%d", done, n)))
	})
	if err != nil {
		live.Finish(a.failLine("Latency", err))
		return err
	}
	a.rep.Latency = &LatencyInfo{Method: "tcp-connect", Stats: stats.Summarize(samples), Failed: failed}
	live.Finish(a.latencyLines()...)
	return nil
}

func (a *App) udpNetwork() string {
	if a.remoteIP.To4() == nil {
		return "udp6"
	}
	return "udp4"
}

func (a *App) resolveLoss(ctx context.Context) (loss.Opener, error) {
	network := a.udpNetwork()
	dnsServer, dnsNet := a.cfg.DNSServer, "udp"
	if dnsServer == "" {
		dnsNet = network
		dnsServer = "1.1.1.1:53"
		if network == "udp6" {
			dnsServer = "[2606:4700:4700::1111]:53"
		}
	} else if _, _, err := net.SplitHostPort(dnsServer); err != nil {
		dnsServer = net.JoinHostPort(dnsServer, "53")
	}
	ip := a.remoteIP
	echo := func() (loss.Conn, error) {
		return loss.OpenEcho(net.JoinHostPort(ip.String(), a.udpPort), network)
	}
	icmp := func() (loss.Conn, error) { return loss.OpenICMP(ip) }
	dns := func() (loss.Conn, error) { return loss.OpenDNS(dnsServer, dnsNet) }

	switch a.cfg.LossMethod {
	case "icmp":
		return icmp, nil
	case "udp":
		return dns, nil
	case "echo":
		if a.udpPort == "" {
			return nil, errors.New("the server does not advertise a UDP echo port (only netspeed servers do)")
		}
		return echo, nil
	}

	var candidates []loss.Opener
	if a.udpPort != "" {
		candidates = append(candidates, echo)
	}
	candidates = append(candidates, icmp, dns)
	var lastErr error
	for _, o := range candidates {
		err := loss.Preflight(ctx, o)
		if err == nil {
			return o, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		lastErr = err
	}
	return nil, fmt.Errorf("no probe method got replies, ICMP and UDP may be blocked (last error: %v)", lastErr)
}

func (a *App) lossPhase(ctx context.Context) error {
	live := a.ui.Live(a.label("Packet loss"))
	live.Set(a.ui.Dim("choosing probe method"))
	opener, err := a.resolveLoss(ctx)
	if err != nil {
		live.Finish(a.failLine("Packet loss", err))
		return err
	}
	c, err := opener()
	if err != nil {
		live.Finish(a.failLine("Packet loss", err))
		return err
	}
	a.lossOpen = opener
	n := a.cfg.LossCount
	method := c.Method()
	res := loss.Run(ctx, c, n, a.cfg.LossInterval, 1500*time.Millisecond, func(p loss.Progress) {
		filled, empty := ui.Bar(float64(p.Sent)/float64(n), 20)
		live.Set(fmt.Sprintf("%s%s  %s", a.ui.Cyan(filled), a.ui.Dim(empty),
			a.ui.Dim(fmt.Sprintf("%s  sent %d/%d  received %d", method, p.Sent, n, p.Received))))
	})
	if ctx.Err() != nil {
		live.Finish()
		return ctx.Err()
	}
	a.rep.PacketLoss = &res
	live.Finish(a.lossLines()...)
	return nil
}

func (a *App) transferPhase(ctx context.Context, dir throughput.Direction) error {
	name := "Download"
	paint := a.ui.Cyan
	if dir == throughput.Upload {
		name = "Upload"
		paint = a.ui.Magenta
	}
	live := a.ui.Live(a.label(name))
	live.Set(a.ui.Dim(fmt.Sprintf("opening %d streams", a.cfg.Connections)))

	warmup := min(max(a.cfg.Duration/5, time.Second), 3*time.Second)
	start := time.Now()
	mon := probe.StartMonitor(ctx, a.pinger, 250*time.Millisecond)
	var bg *loss.Session
	if a.lossOpen != nil {
		if c, err := a.lossOpen(); err == nil {
			bg = loss.Start(ctx, c, 0, 50*time.Millisecond, time.Second, nil)
		}
	}

	res, err := throughput.Run(ctx, throughput.Config{
		Direction:  dir,
		DownURL:    a.downURL,
		UpURL:      a.upURL(),
		Streams:    a.cfg.Connections,
		Duration:   a.cfg.Duration,
		Warmup:     warmup,
		ChunkBytes: int64(a.cfg.ChunkMB) * 1_000_000,
		Transport:  a.newTransport,
		UserAgent:  a.ua,
		OnTick: func(t throughput.Tick) {
			filled, empty := ui.Bar(t.Fraction, 20)
			status := fmt.Sprintf("%s  %s%s  %s", a.ui.Bold(paint(fmt.Sprintf("%11s", fmtRate(t.Bps, a.cfg.Bytes)))),
				paint(filled), a.ui.Dim(empty), a.ui.Dim(fmt.Sprintf("%3.0f%%", t.Fraction*100)))
			if last := mon.Last(); last > 0 {
				status += a.ui.Dim("  ping " + fmtMs(last))
			}
			live.Set(status)
		},
	})
	samples, timeouts := mon.Stop()
	var lr *loss.Result
	if bg != nil {
		r := bg.Stop()
		lr = &r
	}
	if err != nil {
		if ctx.Err() != nil {
			live.Finish()
			return ctx.Err()
		}
		live.Finish(a.failLine(name, err))
		return err
	}

	info := &TransferInfo{
		Mbps:           res.Bps / 1e6,
		PeakMbps:       res.PeakBps / 1e6,
		Bytes:          res.Bytes,
		DurationSec:    res.Elapsed.Seconds(),
		Streams:        res.Streams,
		Errors:         res.Errors,
		LoadedTimeouts: timeouts,
		LoadedLoss:     lr,
		series:         res.Series,
	}
	cutoff := start.Add(warmup / 2)
	var loaded []float64
	for _, s := range samples {
		if s.At.After(cutoff) {
			loaded = append(loaded, s.RTT)
		}
	}
	if len(loaded) == 0 {
		for _, s := range samples {
			loaded = append(loaded, s.RTT)
		}
	}
	if len(loaded) > 0 {
		sum := stats.Summarize(loaded)
		info.LoadedLatency = &sum
		if a.rep.Latency != nil {
			inc := max(0, sum.Median-a.rep.Latency.Stats.Median)
			info.LatencyIncreaseMs = &inc
		}
	}
	if dir == throughput.Upload {
		a.rep.Upload = info
	} else {
		a.rep.Download = info
	}
	live.Finish(a.transferLines(name, info, paint)...)
	return nil
}

func (a *App) finalize() {
	r := a.rep
	var down, up *float64
	if r.Download != nil {
		down = r.Download.LatencyIncreaseMs
	}
	if r.Upload != nil {
		up = r.Upload.LatencyIncreaseMs
	}
	if down != nil || up != nil {
		worst := 0.0
		for _, v := range []*float64{down, up} {
			if v != nil {
				worst = max(worst, *v)
			}
		}
		r.Bufferbloat = &BufferbloatInfo{
			Grade:              score.BufferbloatGrade(worst),
			IncreaseMs:         worst,
			DownloadIncreaseMs: down,
			UploadIncreaseMs:   up,
		}
	}

	var in score.Inputs
	if r.Download != nil {
		in.DownMbps = score.Of(r.Download.Mbps)
	}
	if r.Upload != nil {
		in.UpMbps = score.Of(r.Upload.Mbps)
	}
	if r.Latency != nil {
		in.LatencyMs = score.Of(r.Latency.Stats.Median)
		in.JitterMs = score.Of(r.Latency.Stats.Jitter)
	}
	if r.PacketLoss != nil {
		in.LossPct = score.Of(r.PacketLoss.LossPct)
	}
	if r.Bufferbloat != nil {
		in.BloatMs = score.Of(r.Bufferbloat.IncreaseMs)
	}
	if in.DownMbps.Known || in.UpMbps.Known || in.LatencyMs.Known {
		r.Quality = score.Rate(in)
	}
}
