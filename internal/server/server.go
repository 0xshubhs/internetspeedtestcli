package server

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/0xshubhs/internetspeedtestcli/internal/loss"
)

type Options struct {
	Listen   string
	UDP      string
	MaxBytes int64
	Name     string
	Version  string
}

var block = func() []byte {
	b := make([]byte, 1<<20)
	_, _ = rand.Read(b)
	return b
}()

func Handler(opts Options, udpPort string) http.Handler {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 1 << 30
	}
	common := func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store, no-transform")
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Expose-Headers", "cf-meta-ip, colo, x-netspeed-server, x-netspeed-udp")
		h.Set("x-netspeed-server", opts.Version)
		h.Set("colo", opts.Name)
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			h.Set("cf-meta-ip", host)
		}
		if udpPort != "" {
			h.Set("x-netspeed-udp", udpPort)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /__down", func(w http.ResponseWriter, r *http.Request) {
		common(w, r)
		n := int64(0)
		if v := r.URL.Query().Get("bytes"); v != "" {
			parsed, err := strconv.ParseInt(v, 10, 64)
			if err != nil || parsed < 0 {
				http.Error(w, "bad bytes parameter", http.StatusBadRequest)
				return
			}
			n = parsed
		}
		if n > opts.MaxBytes {
			http.Error(w, fmt.Sprintf("bytes exceeds limit of %d", opts.MaxBytes), http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
		w.WriteHeader(http.StatusOK)
		for n > 0 {
			chunk := block
			if int64(len(chunk)) > n {
				chunk = chunk[:n]
			}
			wrote, err := w.Write(chunk)
			if err != nil {
				return
			}
			n -= int64(wrote)
		}
	})
	mux.HandleFunc("POST /__up", func(w http.ResponseWriter, r *http.Request) {
		common(w, r)
		n, _ := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, opts.MaxBytes))
		w.Header().Set("cf-meta-upload-bytes", strconv.FormatInt(n, 10))
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		common(w, r)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "netspeed server %s\n\nGET  /__down?bytes=N\nPOST /__up\n", opts.Version)
	})
	return mux
}

func ServeUDPEcho(ctx context.Context, pc net.PacketConn) error {
	stop := context.AfterFunc(ctx, func() { pc.Close() })
	defer stop()
	buf := make([]byte, 2048)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if n < 14 || string(buf[:4]) != loss.EchoMagic {
			continue
		}
		_, _ = pc.WriteTo(buf[:n], addr)
	}
}

func Main(args []string, version string) int {
	fs := flag.NewFlagSet("netspeed serve", flag.ContinueOnError)
	opts := Options{Version: version}
	fs.StringVar(&opts.Listen, "listen", ":8080", "HTTP listen address")
	fs.StringVar(&opts.UDP, "udp", "", "UDP echo listen address for packet loss tests (default: same port as -listen, \"off\" to disable)")
	fs.Int64Var(&opts.MaxBytes, "max-bytes", 1<<30, "largest download or upload accepted per request, in bytes")
	fs.StringVar(&opts.Name, "name", "", "server name reported to clients (default: hostname)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: netspeed serve [flags]\n\nRun a self-hosted speed test server. Point clients at it with:\n  netspeed --server http://<host>:8080\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if opts.Name == "" {
		opts.Name, _ = os.Hostname()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		log.Printf("listen %s: %v", opts.Listen, err)
		return 1
	}

	udpPort := ""
	if opts.UDP != "off" {
		udpAddr := opts.UDP
		if udpAddr == "" {
			_, port, _ := net.SplitHostPort(ln.Addr().String())
			host, _, _ := net.SplitHostPort(opts.Listen)
			udpAddr = net.JoinHostPort(host, port)
		}
		pc, err := net.ListenPacket("udp", udpAddr)
		if err != nil {
			log.Printf("UDP echo disabled: %v", err)
		} else {
			_, udpPort, _ = net.SplitHostPort(pc.LocalAddr().String())
			go func() {
				if err := ServeUDPEcho(ctx, pc); err != nil {
					log.Printf("UDP echo stopped: %v", err)
				}
			}()
			log.Printf("UDP echo listening on %s", pc.LocalAddr())
		}
	}

	srv := &http.Server{
		Handler:           Handler(opts, udpPort),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("netspeed server %s listening on http://%s", version, ln.Addr())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("serve: %v", err)
		return 1
	}
	return 0
}
