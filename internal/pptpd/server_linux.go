package pptpd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"
)

// Config is what the server runs with: the pppd options each call's pppd
// reads, and the most control connections it carries at once.
type Config struct {
	Listen   string // the control connection's address; ":1723"
	Options  string // the options file every call's pppd is given
	MaxConns int
	Pppd     string // pppd's path
	Hostname string // the name the server gives in its Start-Control-Connection-Reply
}

// ReadConfig reads the server's configuration file: an "option" line naming
// pppd's options and a "connections" line, the format pptpd read, so a
// tunnel's files are the same under either.
func ReadConfig(path string) (Config, error) {
	c := Config{Listen: ":1723", MaxConns: 4096, Hostname: "wui"}
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		switch fields[0] {
		case "option":
			c.Options = fields[1]
		case "connections":
			if n, err := strconv.Atoi(fields[1]); err == nil && n > 0 {
				c.MaxConns = n
			}
		case "listen":
			c.Listen = fields[1]
		}
	}
	if err := sc.Err(); err != nil {
		return c, err
	}
	if c.Options == "" {
		return c, fmt.Errorf("%s names no pppd options", path)
	}
	return c, nil
}

// How long things wait.
const (
	// A control connection quiet this long is asked whether it is there; one
	// that does not answer within as long again is closed, and its call with
	// it.
	echoInterval = 60 * time.Second
	// The first message is expected promptly from anything that is a device.
	startTimeout = 15 * time.Second
	// A frame received and not yet acknowledged with one of ours is
	// acknowledged alone after this.
	ackDelay = 100 * time.Millisecond
	// The window this side offers: how many packets the device may have
	// unacknowledged.
	recvWindow = 64
	// The device is given this long to answer the server's stop request.
	stopTimeout = 5 * time.Second
)

// Server is a running PPTP server.
type Server struct {
	cfg Config
	log *slog.Logger
	gre *net.IPConn
	// out writes a call's GRE from the address its device dialed: a server
	// with more than one -- a floating address, a second one -- would
	// otherwise answer from whichever the route picks, and the device drops
	// what does not come from the server it called.
	out *ipv4.PacketConn

	mu    sync.Mutex
	calls map[uint16]*call
	conns int
}

// Run serves until ctx ends; then every call is ended and every device told.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	if cfg.Pppd == "" {
		p, err := exec.LookPath("pppd")
		if err != nil {
			return errors.New("pppd is not installed")
		}
		cfg.Pppd = p
	}
	if _, err := os.Stat(cfg.Options); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	// Every GRE packet to this machine; each is a call's by the id in it.
	// By number: the name is looked up in /etc/protocols, which a minimal
	// system does not have.
	gre, err := net.ListenIP("ip4:47", nil)
	if err != nil {
		return fmt.Errorf("opening GRE: %w", err)
	}
	defer gre.Close()
	s := &Server{cfg: cfg, log: log, gre: gre, out: ipv4.NewPacketConn(gre), calls: map[uint16]*call{}}
	go s.readGRE()

	var wg sync.WaitGroup
	conns := map[net.Conn]struct{}{}
	var connsMu sync.Mutex
	go func() {
		<-ctx.Done()
		ln.Close()
		connsMu.Lock()
		for c := range conns {
			c.Close()
		}
		connsMu.Unlock()
	}()
	log.Info("PPTP server listening", "address", cfg.Listen, "options", cfg.Options)
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			// Out of descriptors and the like: wait, rather than spin.
			log.Warn("accept", "error", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		s.mu.Lock()
		full := s.conns >= cfg.MaxConns
		if !full {
			s.conns++
		}
		s.mu.Unlock()
		if full {
			c.Close()
			continue
		}
		connsMu.Lock()
		conns[c] = struct{}{}
		connsMu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.serve(c)
			connsMu.Lock()
			delete(conns, c)
			connsMu.Unlock()
			s.mu.Lock()
			s.conns--
			s.mu.Unlock()
		}()
	}
	wg.Wait()
	return nil
}

// readGRE hands each packet to its call. One that names no call, or comes
// from an address other than its call's device, is dropped.
func (s *Server) readGRE() {
	buf := make([]byte, 65536)
	for {
		n, from, err := s.gre.ReadFromIP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		p, err := parseGRE(buf[:n])
		if err != nil {
			continue
		}
		s.mu.Lock()
		c := s.calls[p.callID]
		s.mu.Unlock()
		if c == nil || !c.peer.Equal(from.IP) {
			continue
		}
		c.received(p)
	}
}

// newCallID picks an id no call has.
func (s *Server) newCallID() uint16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		id := uint16(rand.UintN(65535) + 1)
		if _, taken := s.calls[id]; !taken {
			return id
		}
	}
}

// control is one control connection, with what is written to it kept whole.
type control struct {
	conn net.Conn
	wmu  sync.Mutex
}

func (c *control) send(b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.conn.Write(b)
	return err
}

// serve runs one control connection: the start, one call, the keepalive,
// and the end. A device makes one call per connection; a second is refused.
func (s *Server) serve(nc net.Conn) {
	defer nc.Close()
	peer := nc.RemoteAddr().(*net.TCPAddr).IP.To4()
	local := nc.LocalAddr().(*net.TCPAddr).IP.To4()
	if peer == nil || local == nil {
		return // GRE here is IPv4's
	}
	if tc, ok := nc.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
	}
	c := &control{conn: nc}
	log := s.log.With("peer", peer.String())

	// Messages are read whole by a reader of their own, so nothing the loop
	// below waits for can cut one in half.
	msgs := make(chan message)
	readErr := make(chan error, 1)
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		header := make([]byte, headerLen)
		for {
			if _, err := io.ReadFull(nc, header); err != nil {
				readErr <- err
				return
			}
			typ, length, err := parseHeader(header)
			if err != nil {
				readErr <- err
				return
			}
			b := make([]byte, length-headerLen)
			if _, err := io.ReadFull(nc, b); err != nil {
				readErr <- err
				return
			}
			select {
			case msgs <- message{typ: typ, body: b}:
			case <-quit:
				return
			}
		}
	}()

	var (
		started     bool
		cl          *call
		echoID      uint32
		echoPending bool
	)
	defer func() {
		if cl != nil {
			cl.end()
		}
	}()
	// pppd's end, when it ends on its own, reaches the loop through ended.
	ended := make(chan *call, 1)
	timer := time.NewTimer(startTimeout)
	defer timer.Stop()
	reset := func(d time.Duration) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(d)
	}

	for {
		select {
		case err := <-readErr:
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Info("PPTP connection closed", "reason", err.Error())
			}
			return
		case <-timer.C:
			if !started || echoPending {
				log.Info("PPTP device stopped answering")
				return
			}
			echoID++
			echoPending = true
			if c.send(echoRequest(echoID)) != nil {
				return
			}
			reset(echoInterval)
		case gone := <-ended:
			// The device signed out, the panel disconnected it, or it stopped
			// answering pppd. It is told the call is over, and asked to
			// close; it has a moment to say it has.
			if gone != cl {
				continue
			}
			cl = nil
			_ = c.send(callDisconnect(gone.id, cdnLostCarrier))
			_ = c.send(stopRequest())
			reset(stopTimeout)
			echoPending = true // the next timeout closes the connection
		case m := <-msgs:
			echoPending = false
			reset(echoInterval)
			if !started && m.typ != msgStartReq {
				return // nothing comes before the start
			}
			b := m.body
			switch m.typ {
			case msgStartReq:
				if started {
					return
				}
				started = true
				if c.send(startReply(resultOK, s.cfg.Hostname)) != nil {
					return
				}
			case msgEchoReq:
				if c.send(echoReply(b[0:4])) != nil {
					return
				}
			case msgEchoReply:
			case msgOutCallReq:
				oc := parseOutCall(b)
				if cl != nil {
					_ = c.send(outCallReply(0, oc.peerCallID, callResultRefused, recvWindow))
					continue
				}
				newCall, err := s.startCall(peer, local, oc, log, func(gone *call) {
					select {
					case ended <- gone:
					default:
					}
				})
				if err != nil {
					log.Warn("PPTP call could not start", "error", err)
					_ = c.send(outCallReply(0, oc.peerCallID, callResultErr, recvWindow))
					continue
				}
				cl = newCall
				if c.send(outCallReply(cl.id, oc.peerCallID, callResultOK, recvWindow)) != nil {
					return
				}
			case msgCallClear:
				if cl != nil {
					gone := cl
					cl = nil
					gone.end()
					_ = c.send(callDisconnect(gone.id, cdnAdminShutdown))
				}
			case msgSetLinkInfo, msgWANError:
				// The async map is pppd's and the frames are escaped for any;
				// errors are for the device's own counters.
			case msgStopReq:
				_ = c.send(stopReply())
				return
			case msgStopReply:
				return
			case msgInCallReq, msgInCallReply, msgInCallConn:
				// Calls are made by the device, never to it.
				return
			}
		}
	}
}

// call is one PPTP call: a pppd on a terminal, and the GRE between that
// terminal and the device.
type call struct {
	s      *Server
	id     uint16 // ours: the device's GRE packets carry it
	peerID uint16 // the device's: ours carry it
	peer   net.IP
	local  net.IP // the server's address the device dialed, which its GRE comes from
	master *os.File
	slave  *os.File // held open for the call: the master reads nothing but EIO while no slave is
	cmd    *exec.Cmd
	log    *slog.Logger

	mu         sync.Mutex
	sendSeq    uint32
	recvSeq    uint32
	gotAny     bool
	ackPending bool

	once sync.Once
	done chan struct{}
}

// startCall opens a terminal, starts pppd on it, and relays.
func (s *Server) startCall(peer, local net.IP, oc outCall, log *slog.Logger, onEnd func(*call)) (*call, error) {
	master, slave, err := openPTY()
	if err != nil {
		return nil, err
	}
	slavePath := slave.Name()
	id := s.newCallID()
	cl := &call{
		s: s, id: id, peerID: oc.peerCallID, peer: peer, local: local, master: master, slave: slave,
		log: log.With("call", id), done: make(chan struct{}),
	}
	// The device's address goes to pppd as ipparam: the panel finds a
	// session's device by it, as it did under pptpd.
	cmd := exec.Command(s.cfg.Pppd, slavePath, "file", s.cfg.Options,
		"local", "nodetach", "ipparam", peer.String(), "remotenumber", peer.String())
	cmd.Stdin = nil
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	cl.cmd = cmd

	s.mu.Lock()
	s.calls[id] = cl
	s.mu.Unlock()

	go func() {
		_ = cmd.Wait()
		cl.end()
		onEnd(cl)
	}()
	go cl.fromPPPD()
	go cl.acker()
	cl.log.Info("PPTP call started", "pppd", cmd.Process.Pid)
	return cl, nil
}

// fromPPPD sends what pppd writes to the device, a GRE packet per frame.
func (cl *call) fromPPPD() {
	var d deframer
	buf := make([]byte, 8192)
	out := make([]byte, 0, maxFrame+32)
	dst := &net.IPAddr{IP: cl.peer}
	for {
		n, err := cl.master.Read(buf)
		if err != nil {
			// The terminal closes when pppd ends; its exit ends the call.
			return
		}
		d.write(buf[:n], func(frame []byte) {
			cl.mu.Lock()
			cl.sendSeq++
			p := grePacket{callID: cl.peerID, seq: cl.sendSeq, hasSeq: true, payload: frame}
			if cl.ackPending {
				p.ack, p.hasAck = cl.recvSeq, true
				cl.ackPending = false
			}
			cl.mu.Unlock()
			out = appendGRE(out[:0], p)
			cl.send(out, dst)
		})
	}
}

// send writes a GRE packet to the device, from the address it dialed.
func (cl *call) send(b []byte, dst *net.IPAddr) {
	_, _ = cl.s.out.WriteTo(b, &ipv4.ControlMessage{Src: cl.local}, dst)
}

// received takes one GRE packet from the device: its frame to pppd, and its
// sequence number noted to be acknowledged. A packet older than one already
// passed on is a duplicate or arrived late, and PPP is better without it.
func (cl *call) received(p grePacket) {
	if !p.hasSeq {
		return // an acknowledgement alone; there is no resending to stop
	}
	cl.mu.Lock()
	if cl.gotAny && !seqAfter(p.seq, cl.recvSeq) {
		cl.mu.Unlock()
		return
	}
	cl.recvSeq, cl.gotAny, cl.ackPending = p.seq, true, true
	cl.mu.Unlock()
	if len(p.payload) == 0 {
		return
	}
	_, _ = cl.master.Write(appendFramed(make([]byte, 0, 2*len(p.payload)+8), p.payload))
}

// acker acknowledges what was received when nothing of ours went out to
// carry the acknowledgement: the device stops sending at its window
// otherwise.
func (cl *call) acker() {
	t := time.NewTicker(ackDelay)
	defer t.Stop()
	dst := &net.IPAddr{IP: cl.peer}
	buf := make([]byte, 0, 16)
	for {
		select {
		case <-cl.done:
			return
		case <-t.C:
		}
		cl.mu.Lock()
		if !cl.ackPending {
			cl.mu.Unlock()
			continue
		}
		p := grePacket{callID: cl.peerID, ack: cl.recvSeq, hasAck: true}
		cl.ackPending = false
		cl.mu.Unlock()
		buf = appendGRE(buf[:0], p)
		cl.send(buf, dst)
	}
}

// end ends the call: pppd asked to go, and asked again harder if it does
// not, the terminal closed, and the id freed.
func (cl *call) end() {
	cl.once.Do(func() {
		close(cl.done)
		cl.s.mu.Lock()
		delete(cl.s.calls, cl.id)
		cl.s.mu.Unlock()
		if cl.cmd != nil && cl.cmd.Process != nil {
			_ = cl.cmd.Process.Signal(syscall.SIGTERM)
			p := cl.cmd.Process
			go func() {
				time.Sleep(5 * time.Second)
				_ = p.Signal(syscall.SIGKILL) // nothing if it is gone
			}()
		}
		cl.master.Close()
		cl.slave.Close()
		cl.log.Info("PPTP call ended")
	})
}

// openPTY opens a terminal pair: the master the server relays through, and
// the slave pppd is given by its path. The slave is made raw before pppd has
// it, so nothing the device sends early is echoed back or changed on the way.
func openPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*os.File, *os.File, error) {
		master.Close()
		if slave != nil {
			slave.Close()
		}
		return nil, nil, err
	}
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		return fail(fmt.Errorf("unlocking the terminal: %w", err))
	}
	n, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN)
	if err != nil {
		return fail(fmt.Errorf("naming the terminal: %w", err))
	}
	path := "/dev/pts/" + strconv.FormatUint(uint64(n), 10)
	slave, err = os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fail(err)
	}
	sfd := int(slave.Fd())
	t, err := unix.IoctlGetTermios(sfd, unix.TCGETS)
	if err != nil {
		return fail(err)
	}
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON | unix.IXOFF
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB
	t.Cflag |= unix.CS8
	t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(sfd, unix.TCSETS, t); err != nil {
		return fail(err)
	}
	return master, slave, nil
}
