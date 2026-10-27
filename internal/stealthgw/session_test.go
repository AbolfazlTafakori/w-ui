package stealthgw

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/stealth"
)

// The gateway's session rules, against a stand-in for the tunnel: a socket
// that takes what the gateway forwards and answers whatever the test says.

type bench struct {
	gw     *Gateway
	kernel *net.UDPConn
	anchor stealth.Anchor
}

func newBench(t *testing.T) *bench {
	t.Helper()
	kernel, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { kernel.Close() })
	var priv [32]byte
	priv[0] = 9
	replayFile := filepath.Join(t.TempDir(), "replay")
	if err := writeLimit(replayFile, uint64(time.Now().Unix())-120); err != nil {
		t.Fatal(err)
	}
	gw, err := New(Config{Listen: "127.0.0.1:0", WireGuard: kernel.LocalAddr().String(), PrivateKey: priv, MTU: 1420, ReplayFile: replayFile, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go gw.Serve(ctx)
	return &bench{gw: gw, kernel: kernel, anchor: gw.anchor()}
}

// wgKeepalive is a transport message with nothing in it, under index 7 and
// the given counter.
func wgKeepalive(counter uint64) []byte {
	d := make([]byte, 32)
	d[0] = 4
	binary.LittleEndian.PutUint32(d[4:], 7)
	binary.LittleEndian.PutUint64(d[8:], counter)
	return d
}

// phone is one device at one source address.
type phone struct {
	t    *testing.T
	c    *net.UDPConn
	keys stealth.UDPKeys
	up   *stealth.UDPSender
	down *stealth.UDPReceiver
	size int // of the last opening
}

func (b *bench) phone(t *testing.T, s stealth.Secret) *phone {
	t.Helper()
	c, err := net.DialUDP("udp", nil, b.gw.Addr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &phone{t: t, c: c}
}

func (p *phone) open(b *bench, s stealth.Secret) {
	p.t.Helper()
	pad := stealth.NewPadder(0, 0)
	pk, keys, err := stealth.NewUDPClient(s, b.anchor).Open(nil, time.Now(), wgKeepalive(0), &pad, 1482)
	if err != nil {
		p.t.Fatal(err)
	}
	p.keys, p.up, p.down, p.size = keys, stealth.NewUDPSender(keys.Up, pad), stealth.NewUDPReceiver(keys.Down), len(pk)
	p.c.Write(pk)
}

func (p *phone) send(d []byte) {
	pk, err := p.up.Seal(nil, d, false, 1482)
	if err != nil {
		p.t.Fatal(err)
	}
	p.c.Write(pk)
}

// read is the next packet that opens, its size and its datagram; ok is false
// when none came within wait.
func (p *phone) read(wait time.Duration) (size int, d []byte, ok bool) {
	buf := make([]byte, 65536)
	p.c.SetReadDeadline(time.Now().Add(wait))
	for {
		n, err := p.c.Read(buf)
		if err != nil {
			return 0, nil, false
		}
		if d, err := p.down.Open(buf[:n], nil); err == nil {
			return n, append([]byte(nil), d...), true
		}
	}
}

// heard is the next datagram the tunnel was given, and where from.
func (b *bench) heard(t *testing.T) ([]byte, *net.UDPAddr) {
	t.Helper()
	buf := make([]byte, 65536)
	b.kernel.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, from, err := b.kernel.ReadFromUDP(buf)
	if err != nil {
		t.Fatal("the tunnel was given nothing")
	}
	return buf[:n], from
}

func TestTheFirstAnswerIsNoLargerThanTheOpening(t *testing.T) {
	b := newBench(t)
	s := secret(t)
	var pub [32]byte
	pub[0] = 1
	b.gw.SetDevices([]Device{{PublicKey: pub, Secret: s}})
	p := b.phone(t, s)
	p.open(b, s)
	d, slotAddr := b.heard(t)
	if !isKeepalive(d) {
		t.Fatal("the opening's keepalive did not reach the tunnel")
	}
	// Nothing goes back on a session the tunnel has not spoken on.
	if _, _, ok := p.read(300 * time.Millisecond); ok {
		t.Fatal("a pending session answered")
	}
	// The tunnel speaks: a datagram too large for the first answer.
	big := make([]byte, 1400) // more than any opening of a keepalive holds
	big[0] = 4
	b.kernel.WriteToUDP(big, slotAddr)
	size, got, ok := p.read(2 * time.Second)
	if !ok || got != nil || size > p.size {
		t.Fatalf("first answer: %d bytes after an opening of %d, datagram %v", size, p.size, got != nil)
	}
	// Then the tunnel's datagrams go whole, within the path.
	b.kernel.WriteToUDP(big, slotAddr)
	if size, got, ok := p.read(2 * time.Second); !ok || len(got) != 1400 || size > 1420+stealth.UDPOverhead {
		t.Fatalf("second answer: %d bytes, %d of datagram", size, len(got))
	}
	full := make([]byte, 1420+32)
	full[0] = 4
	b.kernel.WriteToUDP(full, slotAddr)
	if size, got, ok := p.read(2 * time.Second); !ok || len(got) != len(full) || size != 1420+stealth.UDPOverhead {
		t.Fatalf("a full-size datagram left in %d bytes", size)
	}
}

func TestAKeepaliveIsEchoed(t *testing.T) {
	b := newBench(t)
	s := secret(t)
	var pub [32]byte
	pub[0] = 1
	b.gw.SetDevices([]Device{{PublicKey: pub, Secret: s}})
	p := b.phone(t, s)
	p.open(b, s)
	_, slotAddr := b.heard(t)
	b.kernel.WriteToUDP(make([]byte, 64), slotAddr) // any word establishes
	if _, _, ok := p.read(2 * time.Second); !ok {
		t.Fatal("no first answer")
	}
	p.send(wgKeepalive(1))
	b.heard(t)
	if _, d, ok := p.read(echoDelay + time.Second); !ok || d != nil {
		t.Fatal("a keepalive was not echoed with padding alone")
	}
	// A keepalive sent again is a copy: it reaches the tunnel, which drops
	// it, and is not echoed.
	p.send(wgKeepalive(1))
	b.heard(t)
	if _, _, ok := p.read(echoDelay + 500*time.Millisecond); ok {
		t.Fatal("a repeated keepalive was echoed")
	}
}

func TestASourceHoldsFourDevicesAndADeviceTwoPending(t *testing.T) {
	b := newBench(t)
	var devs []Device
	for i := range 5 {
		var pub [32]byte
		pub[0] = byte(i + 1)
		devs = append(devs, Device{PublicKey: pub, Secret: secret(t)})
	}
	b.gw.SetDevices(devs)
	// Five devices behind one address and port.
	p := b.phone(t, devs[0].Secret)
	for _, d := range devs {
		p.open(b, d.Secret)
	}
	waitFor(t, func() bool { st := b.gw.Stats(); return st.Opened+st.SourceDevices == 5 })
	if st := b.gw.Stats(); st.Opened != 4 || st.SourceDevices != 1 {
		t.Fatalf("five devices at one source: %+v", st)
	}
	// One device from three more sources: the second pending is the last.
	for range 3 {
		b.phone(t, devs[4].Secret).open(b, devs[4].Secret)
	}
	waitFor(t, func() bool { st := b.gw.Stats(); return st.Opened+st.SourceDevices+st.Pending == 8 })
	if st := b.gw.Stats(); st.Opened != 6 || st.Pending != 1 {
		t.Fatalf("one device from three sources: %+v", st)
	}
}

func TestAPendingSessionEndsAtItsDeadlineAndAReplacedOneIsReadAWhile(t *testing.T) {
	b := newBench(t)
	s := secret(t)
	var pub [32]byte
	pub[0] = 1
	b.gw.SetDevices([]Device{{PublicKey: pub, Secret: s}})
	p := b.phone(t, s)
	p.open(b, s)
	b.heard(t)
	b.gw.sweep(time.Now().Add(pendingFor))
	if st := b.gw.Stats(); st.Slots != 0 {
		t.Fatalf("a session the tunnel never spoke on outlived its deadline: %+v", st)
	}

	// A live session, then a new one from the same source.
	p.open(b, s)
	_, slotAddr := b.heard(t)
	b.kernel.WriteToUDP(make([]byte, 64), slotAddr)
	p.read(2 * time.Second)
	first := *p
	p.open(b, s)
	b.heard(t)
	b.kernel.WriteToUDP(make([]byte, 64), slotAddr)
	p.read(2 * time.Second)
	// The replaced session's packets still reach the tunnel.
	first.send(wgKeepalive(5))
	if d, _ := b.heard(t); !isKeepalive(d) {
		t.Fatal("the replaced session was not read")
	}
	b.gw.mu.Lock()
	var src netip.AddrPort
	for a := range b.gw.slots {
		src = a
	}
	s0 := b.gw.slots[src][0]
	b.gw.mu.Unlock()
	b.gw.sweep(time.Now().Add(oldReadable))
	if s0.old != nil {
		t.Fatal("the replaced session was read past its time")
	}
}

// A device that sends openings as fast as it can fills the replay cache no
// faster than its two allowances: one for sessions, one for salts spent.
func TestOneDeviceCannotFillTheReplayCache(t *testing.T) {
	b := newBench(t)
	s := secret(t)
	var pub [32]byte
	pub[0] = 1
	b.gw.SetDevices([]Device{{PublicKey: pub, Secret: s}})
	p := b.phone(t, s)
	for range 200 {
		p.open(b, s)
	}
	waitFor(t, func() bool { st := b.gw.Stats(); return st.Opened+st.DeviceRate == 200 })
	st := b.gw.Stats()
	if st.Opened > uint64(deviceProofs.burst)+2 || st.ReplaySalts > int(deviceProofs.burst+deviceSpent.burst)+4 {
		t.Fatalf("200 openings at once: %+v", st)
	}
}
