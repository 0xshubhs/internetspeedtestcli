package loss

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const EchoMagic = "NSPE"

func newToken() [8]byte {
	var t [8]byte
	_, _ = rand.Read(t[:])
	return t
}

type icmpConn struct {
	pc         *icmp.PacketConn
	dst        net.Addr
	ip         net.IP
	v6         bool
	privileged bool
	id         int
	token      [8]byte
}

func OpenICMP(ip net.IP) (Conn, error) {
	v6 := ip.To4() == nil
	network, laddr, raw := "udp4", "0.0.0.0", "ip4:icmp"
	if v6 {
		network, laddr, raw = "udp6", "::", "ip6:ipv6-icmp"
	}
	c := &icmpConn{ip: ip, v6: v6, id: os.Getpid() & 0xFFFF, token: newToken()}
	pc, err := icmp.ListenPacket(network, laddr)
	if err == nil {
		c.pc = pc
		c.dst = &net.UDPAddr{IP: ip}
		return c, nil
	}
	pc, rawErr := icmp.ListenPacket(raw, laddr)
	if rawErr != nil {
		return nil, fmt.Errorf("open ICMP socket: %w", err)
	}
	c.pc = pc
	c.privileged = true
	c.dst = &net.IPAddr{IP: ip}
	return c, nil
}

func (c *icmpConn) Method() string { return "icmp" }
func (c *icmpConn) Target() string { return c.ip.String() }
func (c *icmpConn) Close() error   { return c.pc.Close() }

func (c *icmpConn) Send(seq uint16) error {
	data := make([]byte, 56)
	copy(data, c.token[:])
	var typ icmp.Type = ipv4.ICMPTypeEcho
	if c.v6 {
		typ = ipv6.ICMPTypeEchoRequest
	}
	msg := icmp.Message{Type: typ, Body: &icmp.Echo{ID: c.id, Seq: int(seq), Data: data}}
	b, err := msg.Marshal(nil)
	if err != nil {
		return err
	}
	_, err = c.pc.WriteTo(b, c.dst)
	return err
}

func (c *icmpConn) Recv(buf []byte) (uint16, bool, error) {
	n, _, err := c.pc.ReadFrom(buf)
	if err != nil {
		return 0, false, err
	}
	pkt := buf[:n]
	proto := 1
	var reply icmp.Type = ipv4.ICMPTypeEchoReply
	if c.v6 {
		proto = 58
		reply = ipv6.ICMPTypeEchoReply
	} else if len(pkt) >= 20 && pkt[0]>>4 == 4 {
		hl := int(pkt[0]&0x0f) * 4
		if hl >= 20 && hl <= len(pkt) {
			pkt = pkt[hl:]
		}
	}
	m, err := icmp.ParseMessage(proto, pkt)
	if err != nil || m.Type != reply {
		return 0, false, nil
	}
	echo, ok := m.Body.(*icmp.Echo)
	if !ok || !bytes.HasPrefix(echo.Data, c.token[:]) {
		return 0, false, nil
	}
	if c.privileged && echo.ID != c.id {
		return 0, false, nil
	}
	return uint16(echo.Seq), true, nil
}

type dnsConn struct {
	c      *net.UDPConn
	target string
	base   uint16
	qname  []byte
}

func OpenDNS(server, network string) (Conn, error) {
	if network == "" {
		network = "udp"
	}
	addr, err := net.ResolveUDPAddr(network, server)
	if err != nil {
		return nil, fmt.Errorf("resolve DNS server %s: %w", server, err)
	}
	uc, err := net.DialUDP(network, nil, addr)
	if err != nil {
		return nil, fmt.Errorf("open UDP socket: %w", err)
	}
	t := newToken()
	return &dnsConn{
		c:      uc,
		target: addr.String(),
		base:   binary.BigEndian.Uint16(t[:2]),
		qname:  encodeName("cloudflare.com"),
	}, nil
}

func encodeName(name string) []byte {
	var b []byte
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	return append(b, 0)
}

func (c *dnsConn) Method() string { return "udp-dns" }
func (c *dnsConn) Target() string { return c.target }
func (c *dnsConn) Close() error   { return c.c.Close() }

func (c *dnsConn) Send(seq uint16) error {
	q := make([]byte, 12, 12+len(c.qname)+4)
	binary.BigEndian.PutUint16(q[0:], seq+c.base)
	binary.BigEndian.PutUint16(q[2:], 0x0100)
	binary.BigEndian.PutUint16(q[4:], 1)
	q = append(q, c.qname...)
	q = append(q, 0, 1, 0, 1)
	_, err := c.c.Write(q)
	return err
}

func (c *dnsConn) Recv(buf []byte) (uint16, bool, error) {
	n, err := c.c.Read(buf)
	if err != nil {
		return 0, false, err
	}
	if n < 12 || buf[2]&0x80 == 0 {
		return 0, false, nil
	}
	return binary.BigEndian.Uint16(buf[0:]) - c.base, true, nil
}

type echoConn struct {
	c      *net.UDPConn
	target string
	token  [8]byte
}

func OpenEcho(addr, network string) (Conn, error) {
	if network == "" {
		network = "udp"
	}
	ua, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return nil, fmt.Errorf("resolve UDP echo %s: %w", addr, err)
	}
	uc, err := net.DialUDP(network, nil, ua)
	if err != nil {
		return nil, fmt.Errorf("open UDP socket: %w", err)
	}
	return &echoConn{c: uc, target: ua.String(), token: newToken()}, nil
}

func (c *echoConn) Method() string { return "udp-echo" }
func (c *echoConn) Target() string { return c.target }
func (c *echoConn) Close() error   { return c.c.Close() }

func (c *echoConn) Send(seq uint16) error {
	p := make([]byte, 64)
	copy(p, EchoMagic)
	copy(p[4:], c.token[:])
	binary.BigEndian.PutUint16(p[12:], seq)
	_, err := c.c.Write(p)
	return err
}

func (c *echoConn) Recv(buf []byte) (uint16, bool, error) {
	n, err := c.c.Read(buf)
	if err != nil {
		return 0, false, err
	}
	if n < 14 || string(buf[:4]) != EchoMagic || !bytes.Equal(buf[4:12], c.token[:]) {
		return 0, false, nil
	}
	return binary.BigEndian.Uint16(buf[12:]), true, nil
}

type Opener func() (Conn, error)

var ErrUnreachable = errors.New("no probe replies received")

func Preflight(ctx context.Context, open Opener) error {
	c, err := open()
	if err != nil {
		return err
	}
	r := Run(ctx, c, 3, 50*time.Millisecond, time.Second, nil)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.Received == 0 {
		return ErrUnreachable
	}
	return nil
}
