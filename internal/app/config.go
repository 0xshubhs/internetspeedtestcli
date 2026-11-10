package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

const DefaultServer = "https://speed.cloudflare.com"

type Config struct {
	Server       string
	Duration     time.Duration
	Connections  int
	LatencyCount int
	LossMethod   string
	LossCount    int
	LossInterval time.Duration
	DNSServer    string
	ChunkMB      int
	NoDownload   bool
	NoUpload     bool
	NoLatency    bool
	NoLoss       bool
	IPv4         bool
	IPv6         bool
	JSON         bool
	CSV          bool
	CSVHeader    bool
	Simple       bool
	Bytes        bool
	NoColor      bool
	Version      bool
}

func (c Config) IPVersion() int {
	switch {
	case c.IPv4:
		return 4
	case c.IPv6:
		return 6
	}
	return 0
}

func (c Config) Pretty() bool {
	return !c.JSON && !c.CSV && !c.Simple
}

const usageText = `netspeed: measure your internet connection from the terminal

Usage:
  netspeed [flags]
  netspeed serve [flags]     run a self-hosted test server (see: netspeed serve -h)

Measures download and upload throughput, idle and loaded latency, jitter,
packet loss, bufferbloat, and connection setup time (DNS, TCP, TLS, TTFB).

Test flags:
  -s, --server URL         speed test server (default https://speed.cloudflare.com)
  -d, --duration D         length of each transfer test (default 10s)
  -c, --connections N      parallel TCP streams per transfer test (default 8)
      --chunk-mb N         size of each HTTP transfer request in MB (default 25)
      --latency-count N    idle latency samples (default 20)
      --loss-method M      auto | icmp | udp | echo | off (default auto)
                             icmp: ICMP echo to the test server
                             udp:  UDP DNS queries to --dns-server
                             echo: UDP echo on a netspeed server
      --loss-count N       packets sent in the idle packet loss test (default 100)
      --loss-interval D    gap between loss probes (default 20ms)
      --dns-server ADDR    resolver used for UDP DNS probes (default 1.1.1.1:53)
      --no-download        skip the download test
      --no-upload          skip the upload test
      --no-latency         skip the idle latency test
      --no-loss            skip packet loss measurement
  -4, -6                   use IPv4 or IPv6 only

Output flags:
      --json               print the full report as JSON
      --csv                print the result as one CSV row
      --csv-header         include the header row with --csv
      --simple             print a short plain-text summary
      --bytes              show speeds in bytes per second instead of bits
      --no-color           disable colors (NO_COLOR is honored too)
  -v, --version            print the version and exit
  -h, --help               show this help

Examples:
  netspeed
  netspeed -d 20s -c 16
  netspeed --json > result.json
  netspeed --csv --csv-header >> history.csv
  netspeed --server http://192.168.1.10:8080
`

func parseFlags(args []string) (Config, int, bool) {
	var c Config
	fs := flag.NewFlagSet("netspeed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	str := func(p *string, def string, names ...string) {
		for _, n := range names {
			fs.StringVar(p, n, def, "")
		}
	}
	dur := func(p *time.Duration, def time.Duration, names ...string) {
		for _, n := range names {
			fs.DurationVar(p, n, def, "")
		}
	}
	num := func(p *int, def int, names ...string) {
		for _, n := range names {
			fs.IntVar(p, n, def, "")
		}
	}
	boolean := func(p *bool, names ...string) {
		for _, n := range names {
			fs.BoolVar(p, n, false, "")
		}
	}

	str(&c.Server, DefaultServer, "server", "s")
	dur(&c.Duration, 10*time.Second, "duration", "d")
	num(&c.Connections, 8, "connections", "c")
	num(&c.ChunkMB, 25, "chunk-mb")
	num(&c.LatencyCount, 20, "latency-count")
	str(&c.LossMethod, "auto", "loss-method")
	num(&c.LossCount, 100, "loss-count")
	dur(&c.LossInterval, 20*time.Millisecond, "loss-interval")
	str(&c.DNSServer, "", "dns-server")
	boolean(&c.NoDownload, "no-download")
	boolean(&c.NoUpload, "no-upload")
	boolean(&c.NoLatency, "no-latency")
	boolean(&c.NoLoss, "no-loss")
	boolean(&c.IPv4, "4", "ipv4")
	boolean(&c.IPv6, "6", "ipv6")
	boolean(&c.JSON, "json")
	boolean(&c.CSV, "csv")
	boolean(&c.CSVHeader, "csv-header")
	boolean(&c.Simple, "simple")
	boolean(&c.Bytes, "bytes")
	boolean(&c.NoColor, "no-color")
	boolean(&c.Version, "version", "v")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, usageText)
			return c, 0, true
		}
		return c, usageError(err.Error()), true
	}
	if fs.NArg() > 0 {
		return c, usageError(fmt.Sprintf("unexpected argument %q", fs.Arg(0))), true
	}
	if c.Version {
		return c, 0, false
	}

	switch {
	case c.IPv4 && c.IPv6:
		return c, usageError("-4 and -6 cannot be combined"), true
	case c.Duration < 2*time.Second:
		return c, usageError("--duration must be at least 2s"), true
	case c.Connections < 1 || c.Connections > 64:
		return c, usageError("--connections must be between 1 and 64"), true
	case c.ChunkMB < 1 || c.ChunkMB > 1000:
		return c, usageError("--chunk-mb must be between 1 and 1000"), true
	case c.LatencyCount < 1:
		return c, usageError("--latency-count must be at least 1"), true
	case c.LossCount < 1 || c.LossCount > 60000:
		return c, usageError("--loss-count must be between 1 and 60000"), true
	case c.LossInterval < time.Millisecond:
		return c, usageError("--loss-interval must be at least 1ms"), true
	case boolCount(c.JSON, c.CSV, c.Simple) > 1:
		return c, usageError("choose only one of --json, --csv, --simple"), true
	}
	c.LossMethod = strings.ToLower(c.LossMethod)
	switch c.LossMethod {
	case "auto", "icmp", "udp", "echo", "off":
	case "dns", "udp-dns":
		c.LossMethod = "udp"
	case "udp-echo":
		c.LossMethod = "echo"
	default:
		return c, usageError(fmt.Sprintf("unknown --loss-method %q", c.LossMethod)), true
	}
	if c.LossMethod == "off" {
		c.NoLoss = true
	}
	if c.NoDownload && c.NoUpload && c.NoLatency && c.NoLoss {
		return c, usageError("every test is disabled, nothing to do"), true
	}
	return c, 0, false
}

func boolCount(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

func usageError(msg string) int {
	fmt.Fprintf(os.Stderr, "netspeed: %s\nRun 'netspeed --help' for usage.\n", msg)
	return 2
}

func parseServer(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultServer
	}
	if !strings.Contains(raw, "://") {
		scheme := "https://"
		if probe, err := url.Parse("//" + raw); err == nil && probe.Port() != "" && probe.Port() != "443" {
			scheme = "http://"
		}
		raw = scheme + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("server URL must use http or https, got %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("server URL %q has no host", raw)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}
