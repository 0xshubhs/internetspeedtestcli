package app

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/0xshubhs/internetspeedtestcli/internal/score"
	"github.com/0xshubhs/internetspeedtestcli/internal/ui"
)

const labelWidth = 13

const sep = " · "

func (a *App) label(name string) string {
	return "  " + a.ui.Bold(fmt.Sprintf("%-*s", labelWidth, name))
}

func (a *App) indent() string {
	return strings.Repeat(" ", labelWidth+2)
}

func (a *App) println(s string) {
	if a.pretty {
		a.ui.Println(s)
	}
}

func (a *App) failLine(name string, err error) string {
	if !a.pretty {
		return ""
	}
	return a.label(name) + a.ui.Red("failed: "+err.Error())
}

func fmtMs(v float64) string {
	switch {
	case v < 10:
		return fmt.Sprintf("%.2f ms", v)
	case v < 100:
		return fmt.Sprintf("%.1f ms", v)
	}
	return fmt.Sprintf("%.0f ms", v)
}

func fmtRate(bps float64, inBytes bool) string {
	if inBytes {
		v := bps / 8
		switch {
		case v >= 1e9:
			return fmt.Sprintf("%.2f GB/s", v/1e9)
		case v >= 1e6:
			return fmt.Sprintf("%.2f MB/s", v/1e6)
		case v >= 1e3:
			return fmt.Sprintf("%.1f KB/s", v/1e3)
		}
		return fmt.Sprintf("%.0f B/s", v)
	}
	switch {
	case bps >= 1e9:
		return fmt.Sprintf("%.2f Gbps", bps/1e9)
	case bps >= 1e6:
		return fmt.Sprintf("%.2f Mbps", bps/1e6)
	case bps >= 1e3:
		return fmt.Sprintf("%.1f Kbps", bps/1e3)
	}
	return fmt.Sprintf("%.0f bps", bps)
}

func fmtBytes(n int64) string {
	v := float64(n)
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%.2f GB", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%.1f MB", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.1f KB", v/1e3)
	}
	return fmt.Sprintf("%d B", n)
}

func (a *App) latencyColor(ms float64) func(string) string {
	switch {
	case ms < 30:
		return a.ui.BoldGreen
	case ms < 100:
		return a.ui.BoldYellow
	}
	return a.ui.BoldRed
}

func (a *App) lossColor(pct float64) func(string) string {
	switch {
	case pct == 0:
		return a.ui.BoldGreen
	case pct < 2:
		return a.ui.BoldYellow
	}
	return a.ui.BoldRed
}

func (a *App) gradeColor(g string) func(string) string {
	switch g {
	case "A+", "A":
		return a.ui.BoldGreen
	case "B", "C":
		return a.ui.BoldYellow
	}
	return a.ui.BoldRed
}

func (a *App) levelColor(l score.Level) func(string) string {
	switch l {
	case score.Great:
		return a.ui.BoldGreen
	case score.Good:
		return a.ui.Green
	case score.Fair:
		return a.ui.BoldYellow
	case score.Poor:
		return a.ui.BoldRed
	}
	return a.ui.Dim
}

func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func (a *App) printHeader() {
	if !a.pretty {
		return
	}
	p, r := a.ui, a.rep
	a.println("")
	a.println("  " + p.BoldCyan("netspeed") + " " + p.Dim(a.version) + p.Dim(sep+r.Server.Provider+sep+r.Server.URL))
	a.println("")

	server := r.Server.Provider
	if r.Server.Location != "" {
		server += sep + p.Bold(r.Server.Location)
	}
	a.println(a.label("Server") + server + p.Dim(sep+r.Server.IP))

	place := ""
	if r.Client.City != "" || r.Client.Country != "" {
		place = strings.Trim(r.Client.City+", "+r.Client.Country, ", ")
	}
	client := joinNonEmpty(p.Bold(r.Client.IP), r.Client.ASN, place)
	if r.Client.IP == "" {
		client = joinNonEmpty(r.Client.ASN, place)
	}
	local := fmt.Sprintf("local %s (IPv%d)", r.Client.LocalIP, r.Client.IPVersion)
	if client == "" {
		a.println(a.label("Client") + local)
	} else {
		a.println(a.label("Client") + client + p.Dim(sep+local))
	}

	if c := r.Connection; c != nil {
		parts := []string{}
		if c.DNSMs > 0 {
			parts = append(parts, "DNS "+p.Bold(fmtMs(c.DNSMs)))
		}
		parts = append(parts, "TCP "+p.Bold(fmtMs(c.TCPMs)))
		if c.TLSMs > 0 {
			parts = append(parts, "TLS "+p.Bold(fmtMs(c.TLSMs)))
		}
		parts = append(parts, "TTFB "+p.Bold(fmtMs(c.TTFBMs)))
		a.println(a.label("Connection") + strings.Join(parts, sep))
		detail := []string{c.Protocol}
		if c.TLSVersion != "" {
			detail = append(detail, c.TLSVersion, c.Cipher)
		}
		if c.HTTPRTTMs > 0 {
			detail = append(detail, "HTTP request "+fmtMs(c.HTTPRTTMs))
		}
		a.println(a.indent() + p.Dim(strings.Join(detail, sep)))
	}
	a.println("")
}

func (a *App) latencyLines() []string {
	if !a.pretty || a.rep.Latency == nil {
		return nil
	}
	p, l := a.ui, a.rep.Latency
	s := l.Stats
	main := a.label("Latency") + a.latencyColor(s.Median)(fmtMs(s.Median)) + "   " + p.Dim("jitter ") + p.Bold(fmtMs(s.Jitter))
	detail := fmt.Sprintf("min %s · avg %s · p90 %s · max %s · %d samples",
		fmtMs(s.Min), fmtMs(s.Mean), fmtMs(s.P90), fmtMs(s.Max), s.Count)
	if l.Failed > 0 {
		detail += fmt.Sprintf(" · %d timed out", l.Failed)
	}
	return []string{main, a.indent() + p.Dim(detail)}
}

func (a *App) lossLines() []string {
	if !a.pretty || a.rep.PacketLoss == nil {
		return nil
	}
	p, r := a.ui, a.rep.PacketLoss
	main := a.label("Packet loss") + a.lossColor(r.LossPct)(fmt.Sprintf("%.2f %%", r.LossPct)) + "   " +
		p.Dim(fmt.Sprintf("%d of %d lost", r.Lost, r.Sent))
	detail := fmt.Sprintf("%s to %s", r.Method, r.Target)
	if r.RTT.Count > 0 {
		detail += fmt.Sprintf(" · rtt %s · jitter %s", fmtMs(r.RTT.Median), fmtMs(r.RTT.Jitter))
	}
	if r.Duplicates > 0 {
		detail += fmt.Sprintf(" · %d duplicates", r.Duplicates)
	}
	return []string{main, a.indent() + p.Dim(detail)}
}

func (a *App) transferLines(name string, t *TransferInfo, paint func(string) string) []string {
	if !a.pretty {
		return nil
	}
	p := a.ui
	main := a.label(name) + p.Bold(paint(fmtRate(t.Mbps*1e6, a.cfg.Bytes)))
	if spark := ui.Sparkline(t.series, 28); spark != "" && p.Width() >= 64 {
		main += "  " + paint(spark)
	}
	lines := []string{main}
	lines = append(lines, a.indent()+p.Dim(fmt.Sprintf("peak %s · %s · %d streams · %.1fs",
		fmtRate(t.PeakMbps*1e6, a.cfg.Bytes), fmtBytes(t.Bytes), t.Streams, t.DurationSec)))
	if t.LoadedLatency != nil {
		s := t.LoadedLatency
		line := p.Dim("loaded latency ") + a.latencyColor(s.Median)(fmtMs(s.Median))
		var extra []string
		if t.LatencyIncreaseMs != nil {
			extra = append(extra, "+"+fmtMs(*t.LatencyIncreaseMs))
		}
		extra = append(extra, "p90 "+fmtMs(s.P90))
		if t.LoadedTimeouts > 0 {
			extra = append(extra, fmt.Sprintf("%d timeouts", t.LoadedTimeouts))
		}
		if t.LoadedLoss != nil && t.LoadedLoss.Sent > 0 {
			extra = append(extra, fmt.Sprintf("loss %.2f %%", t.LoadedLoss.LossPct))
		}
		line += p.Dim(sep + strings.Join(extra, sep))
		lines = append(lines, a.indent()+line)
	}
	return lines
}

func (a *App) printSummary() {
	if !a.pretty {
		return
	}
	p, r := a.ui, a.rep
	a.println("")
	if b := r.Bufferbloat; b != nil {
		var parts []string
		if b.DownloadIncreaseMs != nil {
			parts = append(parts, "+"+fmtMs(*b.DownloadIncreaseMs)+" download")
		}
		if b.UploadIncreaseMs != nil {
			parts = append(parts, "+"+fmtMs(*b.UploadIncreaseMs)+" upload")
		}
		a.println(a.label("Bufferbloat") + a.gradeColor(b.Grade)(fmt.Sprintf("%-3s", b.Grade)) + " " + p.Dim(strings.Join(parts, sep)))
	}
	for i, q := range r.Quality {
		prefix := a.indent()
		if i == 0 {
			prefix = a.label("Quality")
		}
		line := prefix + fmt.Sprintf("%-13s", q.UseCase)
		if q.Limit != "" {
			line += a.levelColor(q.Level)(fmt.Sprintf("%-6s", q.Level)) + p.Dim("limited by "+q.Limit)
		} else {
			line += a.levelColor(q.Level)(q.Level.String())
		}
		a.println(line)
	}
	var used []string
	if r.Download != nil {
		used = append(used, fmtBytes(r.Download.Bytes)+" down")
	}
	if r.Upload != nil {
		used = append(used, fmtBytes(r.Upload.Bytes)+" up")
	}
	if len(used) > 0 {
		a.println(a.label("Data used") + p.Dim(strings.Join(used, sep)))
	}
	if r.Bufferbloat != nil || len(r.Quality) > 0 || len(used) > 0 {
		a.println("")
	}
}

func optFloat(v float64, ok bool) string {
	if !ok {
		return ""
	}
	return strconv.FormatFloat(round(v, 3), 'f', -1, 64)
}

func (a *App) writeCSV(w io.Writer) {
	r := a.rep
	cw := csv.NewWriter(w)
	if a.cfg.CSVHeader {
		_ = cw.Write([]string{
			"timestamp", "server", "server_ip", "location", "client_ip", "asn",
			"latency_ms", "jitter_ms", "download_mbps", "upload_mbps", "packet_loss_pct",
			"loaded_latency_down_ms", "loaded_latency_up_ms", "bufferbloat",
			"download_bytes", "upload_bytes", "errors",
		})
	}
	row := []string{
		r.Timestamp.Format(time.RFC3339), r.Server.Host, r.Server.IP, r.Server.Location,
		r.Client.IP, r.Client.ASN,
	}
	var lat, jit float64
	if r.Latency != nil {
		lat, jit = r.Latency.Stats.Median, r.Latency.Stats.Jitter
	}
	row = append(row, optFloat(lat, r.Latency != nil), optFloat(jit, r.Latency != nil))
	var dm, um float64
	if r.Download != nil {
		dm = r.Download.Mbps
	}
	if r.Upload != nil {
		um = r.Upload.Mbps
	}
	row = append(row, optFloat(dm, r.Download != nil), optFloat(um, r.Upload != nil))
	var lp float64
	if r.PacketLoss != nil {
		lp = r.PacketLoss.LossPct
	}
	row = append(row, optFloat(lp, r.PacketLoss != nil))
	loaded := func(t *TransferInfo) string {
		if t == nil || t.LoadedLatency == nil {
			return ""
		}
		return optFloat(t.LoadedLatency.Median, true)
	}
	row = append(row, loaded(r.Download), loaded(r.Upload))
	grade := ""
	if r.Bufferbloat != nil {
		grade = r.Bufferbloat.Grade
	}
	row = append(row, grade)
	var db, ub string
	if r.Download != nil {
		db = strconv.FormatInt(r.Download.Bytes, 10)
	}
	if r.Upload != nil {
		ub = strconv.FormatInt(r.Upload.Bytes, 10)
	}
	row = append(row, db, ub, strings.Join(r.Errors, "; "))
	_ = cw.Write(row)
	cw.Flush()
}

func (a *App) writeSimple(w io.Writer) {
	r := a.rep
	if r.Latency != nil {
		fmt.Fprintf(w, "Latency:     %s (jitter %s)\n", fmtMs(r.Latency.Stats.Median), fmtMs(r.Latency.Stats.Jitter))
	}
	if r.Download != nil {
		fmt.Fprintf(w, "Download:    %s\n", fmtRate(r.Download.Mbps*1e6, a.cfg.Bytes))
	}
	if r.Upload != nil {
		fmt.Fprintf(w, "Upload:      %s\n", fmtRate(r.Upload.Mbps*1e6, a.cfg.Bytes))
	}
	if r.PacketLoss != nil {
		fmt.Fprintf(w, "Packet loss: %.2f %%\n", r.PacketLoss.LossPct)
	}
	if r.Bufferbloat != nil {
		fmt.Fprintf(w, "Bufferbloat: %s (+%s under load)\n", r.Bufferbloat.Grade, fmtMs(r.Bufferbloat.IncreaseMs))
	}
	for _, e := range r.Errors {
		fmt.Fprintf(w, "Error:       %s\n", e)
	}
}
