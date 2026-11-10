package app

import (
	"math"
	"time"

	"github.com/0xshubhs/internetspeedtestcli/internal/loss"
	"github.com/0xshubhs/internetspeedtestcli/internal/score"
	"github.com/0xshubhs/internetspeedtestcli/internal/stats"
)

type Report struct {
	Timestamp   time.Time        `json:"timestamp"`
	Version     string           `json:"version"`
	Server      ServerInfo       `json:"server"`
	Client      ClientInfo       `json:"client"`
	Connection  *ConnectionInfo  `json:"connection,omitempty"`
	Latency     *LatencyInfo     `json:"latency,omitempty"`
	PacketLoss  *loss.Result     `json:"packet_loss,omitempty"`
	Download    *TransferInfo    `json:"download,omitempty"`
	Upload      *TransferInfo    `json:"upload,omitempty"`
	Bufferbloat *BufferbloatInfo `json:"bufferbloat,omitempty"`
	Quality     []score.Rating   `json:"quality,omitempty"`
	Errors      []string         `json:"errors,omitempty"`
}

type ServerInfo struct {
	URL      string `json:"url"`
	Host     string `json:"host"`
	IP       string `json:"ip,omitempty"`
	Provider string `json:"provider"`
	Location string `json:"location,omitempty"`
}

type ClientInfo struct {
	IP        string `json:"ip,omitempty"`
	ASN       string `json:"asn,omitempty"`
	City      string `json:"city,omitempty"`
	Country   string `json:"country,omitempty"`
	LocalIP   string `json:"local_ip,omitempty"`
	IPVersion int    `json:"ip_version,omitempty"`
}

type ConnectionInfo struct {
	DNSMs      float64 `json:"dns_ms"`
	TCPMs      float64 `json:"tcp_connect_ms"`
	TLSMs      float64 `json:"tls_handshake_ms,omitempty"`
	TTFBMs     float64 `json:"ttfb_ms"`
	HTTPRTTMs  float64 `json:"http_rtt_ms,omitempty"`
	Protocol   string  `json:"protocol"`
	TLSVersion string  `json:"tls_version,omitempty"`
	Cipher     string  `json:"tls_cipher,omitempty"`
}

type LatencyInfo struct {
	Method string        `json:"method"`
	Stats  stats.Summary `json:"ms"`
	Failed int           `json:"failed"`
}

type TransferInfo struct {
	Mbps              float64        `json:"mbps"`
	PeakMbps          float64        `json:"peak_mbps"`
	Bytes             int64          `json:"bytes"`
	DurationSec       float64        `json:"duration_sec"`
	Streams           int            `json:"streams"`
	Errors            int            `json:"request_errors"`
	LoadedLatency     *stats.Summary `json:"loaded_latency_ms,omitempty"`
	LatencyIncreaseMs *float64       `json:"latency_increase_ms,omitempty"`
	LoadedTimeouts    int            `json:"loaded_latency_timeouts"`
	LoadedLoss        *loss.Result   `json:"loaded_packet_loss,omitempty"`
	series            []float64
}

type BufferbloatInfo struct {
	Grade              string   `json:"grade"`
	IncreaseMs         float64  `json:"increase_ms"`
	DownloadIncreaseMs *float64 `json:"download_increase_ms,omitempty"`
	UploadIncreaseMs   *float64 `json:"upload_increase_ms,omitempty"`
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

func roundSummary(s stats.Summary) stats.Summary {
	return stats.Summary{
		Count:  s.Count,
		Min:    round(s.Min, 3),
		Mean:   round(s.Mean, 3),
		Median: round(s.Median, 3),
		P90:    round(s.P90, 3),
		Max:    round(s.Max, 3),
		StdDev: round(s.StdDev, 3),
		Jitter: round(s.Jitter, 3),
	}
}

func roundLoss(r *loss.Result) *loss.Result {
	if r == nil {
		return nil
	}
	c := *r
	c.LossPct = round(c.LossPct, 3)
	c.RTT = roundSummary(c.RTT)
	return &c
}

func (r *Report) rounded() *Report {
	c := *r
	if r.Connection != nil {
		ci := *r.Connection
		ci.DNSMs = round(ci.DNSMs, 3)
		ci.TCPMs = round(ci.TCPMs, 3)
		ci.TLSMs = round(ci.TLSMs, 3)
		ci.TTFBMs = round(ci.TTFBMs, 3)
		ci.HTTPRTTMs = round(ci.HTTPRTTMs, 3)
		c.Connection = &ci
	}
	if r.Latency != nil {
		li := *r.Latency
		li.Stats = roundSummary(li.Stats)
		c.Latency = &li
	}
	c.PacketLoss = roundLoss(r.PacketLoss)
	roundTransfer := func(t *TransferInfo) *TransferInfo {
		if t == nil {
			return nil
		}
		ti := *t
		ti.Mbps = round(ti.Mbps, 3)
		ti.PeakMbps = round(ti.PeakMbps, 3)
		ti.DurationSec = round(ti.DurationSec, 3)
		if ti.LoadedLatency != nil {
			s := roundSummary(*ti.LoadedLatency)
			ti.LoadedLatency = &s
		}
		if ti.LatencyIncreaseMs != nil {
			v := round(*ti.LatencyIncreaseMs, 3)
			ti.LatencyIncreaseMs = &v
		}
		ti.LoadedLoss = roundLoss(ti.LoadedLoss)
		return &ti
	}
	c.Download = roundTransfer(r.Download)
	c.Upload = roundTransfer(r.Upload)
	if r.Bufferbloat != nil {
		b := *r.Bufferbloat
		b.IncreaseMs = round(b.IncreaseMs, 3)
		if b.DownloadIncreaseMs != nil {
			v := round(*b.DownloadIncreaseMs, 3)
			b.DownloadIncreaseMs = &v
		}
		if b.UploadIncreaseMs != nil {
			v := round(*b.UploadIncreaseMs, 3)
			b.UploadIncreaseMs = &v
		}
		c.Bufferbloat = &b
	}
	return &c
}
