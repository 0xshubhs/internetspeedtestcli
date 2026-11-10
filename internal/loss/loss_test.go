package loss_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/0xshubhs/internetspeedtestcli/internal/loss"
	"github.com/0xshubhs/internetspeedtestcli/internal/server"
)

type fakeConn struct {
	drop    func(seq uint16) bool
	dup     func(seq uint16) bool
	replies chan uint16
	closed  chan struct{}
	once    sync.Once
}

func newFake(drop, dup func(uint16) bool) *fakeConn {
	return &fakeConn{drop: drop, dup: dup, replies: make(chan uint16, 1024), closed: make(chan struct{})}
}

func (f *fakeConn) Send(seq uint16) error {
	if f.drop != nil && f.drop(seq) {
		return nil
	}
	f.replies <- seq
	if f.dup != nil && f.dup(seq) {
		f.replies <- seq
	}
	return nil
}

func (f *fakeConn) Recv(buf []byte) (uint16, bool, error) {
	select {
	case s := <-f.replies:
		return s, true, nil
	case <-f.closed:
		return 0, false, net.ErrClosed
	}
}

func (f *fakeConn) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeConn) Method() string { return "fake" }
func (f *fakeConn) Target() string { return "nowhere" }

func TestRunCountsLossAndDuplicates(t *testing.T) {
	c := newFake(
		func(s uint16) bool { return s%4 == 0 },
		func(s uint16) bool { return s == 5 },
	)
	r := loss.Run(context.Background(), c, 40, time.Millisecond, 100*time.Millisecond, nil)
	if r.Sent != 40 || r.Received != 30 || r.Lost != 10 || r.Duplicates != 1 {
		t.Fatalf("got %+v, want sent 40 received 30 lost 10 dup 1", r)
	}
	if r.LossPct != 25 {
		t.Fatalf("loss = %v%%, want 25%%", r.LossPct)
	}
	if r.RTT.Count != 30 {
		t.Fatalf("rtt samples = %d, want 30", r.RTT.Count)
	}
}

func TestBackgroundSessionStops(t *testing.T) {
	c := newFake(nil, nil)
	s := loss.Start(context.Background(), c, 0, 2*time.Millisecond, 100*time.Millisecond, nil)
	time.Sleep(50 * time.Millisecond)
	r := s.Stop()
	if r.Sent == 0 || r.Lost != 0 {
		t.Fatalf("background session result %+v, want packets sent and none lost", r)
	}
	if again := s.Stop(); again != r {
		t.Fatalf("second Stop returned %+v, want %+v", again, r)
	}
}

func TestUDPEchoAgainstServer(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot open UDP socket: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.ServeUDPEcho(ctx, pc)

	c, err := loss.OpenEcho(pc.LocalAddr().String(), "udp4")
	if err != nil {
		t.Fatal(err)
	}
	r := loss.Run(ctx, c, 50, time.Millisecond, time.Second, nil)
	if r.Method != "udp-echo" || r.Sent != 50 || r.Lost != 0 {
		t.Fatalf("echo result %+v, want 50 sent and 0 lost", r)
	}
}

func TestPreflightFailsWithoutReplies(t *testing.T) {
	open := func() (loss.Conn, error) {
		return newFake(func(uint16) bool { return true }, nil), nil
	}
	err := loss.Preflight(context.Background(), open)
	if !errors.Is(err, loss.ErrUnreachable) {
		t.Fatalf("Preflight error = %v, want ErrUnreachable", err)
	}
}
