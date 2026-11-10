package probe

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/0xshubhs/internetspeedtestcli/internal/stats"
)

type Setup struct {
	DNS        time.Duration
	TCP        time.Duration
	TLS        time.Duration
	TTFB       time.Duration
	HTTPRTT    []float64
	RemoteIP   net.IP
	LocalIP    net.IP
	Port       string
	Protocol   string
	TLSVersion string
	Cipher     string
	Header     http.Header
}

type SetupConfig struct {
	URL        *url.URL
	ProbeURL   string
	IPVersion  int
	UserAgent  string
	WarmProbes int
	Timeout    time.Duration
}

func MatchesVersion(ip net.IP, version int) bool {
	switch version {
	case 4:
		return ip.To4() != nil
	case 6:
		return ip.To4() == nil
	}
	return true
}

func DefaultPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func MeasureSetup(ctx context.Context, cfg SetupConfig) (*Setup, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	host := cfg.URL.Hostname()
	s := &Setup{Port: DefaultPort(cfg.URL)}

	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		if !MatchesVersion(ip, cfg.IPVersion) {
			return nil, fmt.Errorf("%s is not an IPv%d address", host, cfg.IPVersion)
		}
		ips = []net.IP{ip}
	} else {
		start := time.Now()
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		s.DNS = time.Since(start)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", host, err)
		}
		for _, a := range addrs {
			if MatchesVersion(a.IP, cfg.IPVersion) {
				ips = append(ips, a.IP)
			}
		}
		if len(ips) == 0 {
			if cfg.IPVersion != 0 {
				return nil, fmt.Errorf("%s has no IPv%d address", host, cfg.IPVersion)
			}
			return nil, fmt.Errorf("%s did not resolve to any address", host)
		}
	}

	dialer := net.Dialer{Timeout: 4 * time.Second}
	var conn net.Conn
	var lastErr error
	for _, ip := range ips {
		network := "tcp4"
		if ip.To4() == nil {
			network = "tcp6"
		}
		start := time.Now()
		c, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), s.Port))
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		s.TCP = time.Since(start)
		s.RemoteIP = ip
		conn = c
		break
	}
	if conn == nil {
		return nil, fmt.Errorf("connect to %s: %w", host, lastErr)
	}
	if ta, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		s.LocalIP = ta.IP
	}

	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(cfg.Timeout))

	if cfg.URL.Scheme == "https" {
		tc := tls.Client(conn, &tls.Config{ServerName: host, NextProtos: []string{"http/1.1"}})
		start := time.Now()
		if err := tc.HandshakeContext(ctx); err != nil {
			return nil, fmt.Errorf("TLS handshake with %s: %w", host, err)
		}
		s.TLS = time.Since(start)
		st := tc.ConnectionState()
		s.TLSVersion = tls.VersionName(st.Version)
		s.Cipher = tls.CipherSuiteName(st.CipherSuite)
		conn = tc
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.ProbeURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", cfg.UserAgent)
	br := bufio.NewReader(conn)

	for i := 0; i <= cfg.WarmProbes; i++ {
		start := time.Now()
		if err := req.Write(conn); err != nil {
			if i > 0 {
				break
			}
			return nil, fmt.Errorf("send request: %w", err)
		}
		resp, err := http.ReadResponse(br, req)
		elapsed := time.Since(start)
		if err != nil {
			if i > 0 {
				break
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("read response: %w", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("server answered %s for %s", resp.Status, cfg.ProbeURL)
		}
		if i == 0 {
			s.TTFB = elapsed
			s.Header = resp.Header
			s.Protocol = resp.Proto
		} else {
			s.HTTPRTT = append(s.HTTPRTT, stats.Ms(elapsed))
		}
		if resp.Close {
			break
		}
	}
	return s, nil
}
