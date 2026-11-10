package ui

import (
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

type Printer struct {
	w     io.Writer
	tty   bool
	color bool
	width int

	mu     sync.Mutex
	active *Live
}

func New(f *os.File, noColor bool) *Printer {
	tty := term.IsTerminal(int(f.Fd()))
	width := 80
	if tty {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			width = w
		}
	}
	color := tty && !noColor && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	return &Printer{w: f, tty: tty, color: color, width: width}
}

func Discard() *Printer {
	return &Printer{w: io.Discard, width: 80}
}

func (p *Printer) Width() int { return p.width }
func (p *Printer) TTY() bool  { return p.tty }

func (p *Printer) Println(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintln(p.w, s)
}

func (p *Printer) HideCursor() {
	if p.tty {
		fmt.Fprint(p.w, "\x1b[?25l")
	}
}

func (p *Printer) ShowCursor() {
	if p.tty {
		fmt.Fprint(p.w, "\x1b[?25h")
	}
}

func (p *Printer) Abort() {
	p.mu.Lock()
	l := p.active
	p.mu.Unlock()
	if l != nil {
		l.stopAnim()
		p.mu.Lock()
		if p.tty {
			fmt.Fprint(p.w, "\r\x1b[K")
		}
		p.active = nil
		p.mu.Unlock()
	}
	p.ShowCursor()
}

func (p *Printer) paint(code, s string) string {
	if !p.color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p *Printer) Bold(s string) string        { return p.paint("1", s) }
func (p *Printer) Dim(s string) string         { return p.paint("2", s) }
func (p *Printer) Red(s string) string         { return p.paint("31", s) }
func (p *Printer) Green(s string) string       { return p.paint("32", s) }
func (p *Printer) Yellow(s string) string      { return p.paint("33", s) }
func (p *Printer) Blue(s string) string        { return p.paint("34", s) }
func (p *Printer) Magenta(s string) string     { return p.paint("35", s) }
func (p *Printer) Cyan(s string) string        { return p.paint("36", s) }
func (p *Printer) BoldCyan(s string) string    { return p.paint("1;36", s) }
func (p *Printer) BoldMagenta(s string) string { return p.paint("1;35", s) }
func (p *Printer) BoldGreen(s string) string   { return p.paint("1;32", s) }
func (p *Printer) BoldYellow(s string) string  { return p.paint("1;33", s) }
func (p *Printer) BoldRed(s string) string     { return p.paint("1;31", s) }

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func VisibleLen(s string) int {
	return utf8.RuneCountInString(ansi.ReplaceAllString(s, ""))
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type Live struct {
	p      *Printer
	prefix string
	status string
	frame  int
	mu     sync.Mutex
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

func (p *Printer) Live(prefix string) *Live {
	l := &Live{p: p, prefix: prefix, stop: make(chan struct{}), done: make(chan struct{})}
	p.mu.Lock()
	p.active = l
	p.mu.Unlock()
	if !p.tty {
		close(l.done)
		return l
	}
	go func() {
		defer close(l.done)
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		l.render()
		for {
			select {
			case <-l.stop:
				return
			case <-t.C:
				l.render()
			}
		}
	}()
	return l
}

func (l *Live) Set(status string) {
	l.mu.Lock()
	l.status = status
	l.mu.Unlock()
}

func (l *Live) render() {
	l.mu.Lock()
	l.frame++
	line := l.prefix + l.p.Cyan(spinner[l.frame%len(spinner)]) + " " + l.status
	l.mu.Unlock()
	if VisibleLen(line) >= l.p.width {
		line = truncateVisible(line, l.p.width-1)
	}
	l.p.mu.Lock()
	fmt.Fprint(l.p.w, "\r\x1b[K"+line)
	l.p.mu.Unlock()
}

func (l *Live) stopAnim() {
	l.once.Do(func() { close(l.stop) })
	<-l.done
}

func (l *Live) Finish(lines ...string) {
	l.stopAnim()
	l.p.mu.Lock()
	defer l.p.mu.Unlock()
	if l.p.tty {
		fmt.Fprint(l.p.w, "\r\x1b[K")
	}
	for _, s := range lines {
		fmt.Fprintln(l.p.w, s)
	}
	if l.p.active == l {
		l.p.active = nil
	}
}

func truncateVisible(s string, n int) string {
	plain := ansi.ReplaceAllString(s, "")
	r := []rune(plain)
	if len(r) <= n {
		return plain
	}
	return string(r[:max(0, n)])
}

var sparks = []rune("▁▂▃▄▅▆▇█")

func Sparkline(values []float64, width int) string {
	if len(values) == 0 || width <= 0 {
		return ""
	}
	buckets := make([]float64, 0, width)
	if len(values) <= width {
		buckets = append(buckets, values...)
	} else {
		for i := 0; i < width; i++ {
			lo := i * len(values) / width
			hi := (i + 1) * len(values) / width
			var sum float64
			for _, v := range values[lo:hi] {
				sum += v
			}
			buckets = append(buckets, sum/float64(hi-lo))
		}
	}
	var top float64
	for _, v := range buckets {
		top = math.Max(top, v)
	}
	var b strings.Builder
	for _, v := range buckets {
		idx := 0
		if top > 0 {
			idx = int(math.Round(v / top * float64(len(sparks)-1)))
		}
		b.WriteRune(sparks[max(0, min(idx, len(sparks)-1))])
	}
	return b.String()
}

func Bar(fraction float64, width int) (filled, empty string) {
	fraction = math.Max(0, math.Min(1, fraction))
	n := int(math.Round(fraction * float64(width)))
	return strings.Repeat("█", n), strings.Repeat("░", width-n)
}
