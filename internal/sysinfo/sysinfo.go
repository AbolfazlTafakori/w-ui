// Package sysinfo samples host telemetry for the overview page.
//
// Everything is collected on a background ticker and served from a cached
// snapshot rather than gathered per request. Two reasons: an open dashboard
// polling every few seconds would otherwise re-walk /proc on every poll, and
// rates like throughput are only meaningful as a delta between two samples —
// asking for "current speed" at a single instant has no answer.
package sysinfo

import (
	"context"
	"log/slog"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// Usage is a used-of-total pair with the percentage precomputed, so the
// frontend never has to guess how a ratio was derived.
type Usage struct {
	Used    uint64  `json:"used"`
	Total   uint64  `json:"total"`
	Percent float64 `json:"percent"`
}

func newUsage(used, total uint64) Usage {
	u := Usage{Used: used, Total: total}
	if total > 0 {
		u.Percent = float64(used) / float64(total) * 100
	}
	return u
}

// CPU describes processor load.
type CPU struct {
	Percent float64 `json:"percent"`
	Cores   int     `json:"cores"`
	Model   string  `json:"model"`
	// Load1/5/15 are the Unix load averages. They read 0 on Windows, where the
	// concept does not exist, rather than being faked.
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

// Host describes the machine.
type Host struct {
	OS        string `json:"os"`
	Platform  string `json:"platform"`
	Kernel    string `json:"kernel"`
	Arch      string `json:"arch"`
	Hostname  string `json:"hostname"`
	UptimeSec uint64 `json:"uptimeSec"`
}

// Network holds cumulative counters and the rate between the last two samples.
type Network struct {
	BytesSent uint64 `json:"bytesSent"`
	BytesRecv uint64 `json:"bytesRecv"`
	SentRate  uint64 `json:"sentRate"` // bytes per second
	RecvRate  uint64 `json:"recvRate"`
	TCPConns  int    `json:"tcpConns"`
	UDPConns  int    `json:"udpConns"`
}

// Panel describes the process itself.
type Panel struct {
	MemoryBytes uint64 `json:"memoryBytes"`
	Goroutines  int    `json:"goroutines"`
	Threads     int    `json:"threads"`
	UptimeSec   int64  `json:"uptimeSec"`
}

// History is a rolling window of recent samples, oldest first, for the
// sparklines on the overview.
//
// It is kept server-side rather than accumulated in the browser so the shape of
// the last few minutes survives a page reload, and so it does not depend on how
// often a particular tab happens to poll.
type History struct {
	CPU    []float64 `json:"cpu"`
	Memory []float64 `json:"memory"`
	Swap   []float64 `json:"swap"`
	Disk   []float64 `json:"disk"`
	Up     []uint64  `json:"up"`
	Down   []uint64  `json:"down"`
	TCP    []int     `json:"tcp"`
	UDP    []int     `json:"udp"`
}

// Snapshot is one complete sample.
type Snapshot struct {
	CPU       CPU       `json:"cpu"`
	Memory    Usage     `json:"memory"`
	Swap      Usage     `json:"swap"`
	Disk      Usage     `json:"disk"`
	Host      Host      `json:"host"`
	Network   Network   `json:"network"`
	Panel     Panel     `json:"panel"`
	History   History   `json:"history"`
	IPv4      []string  `json:"ipv4"`
	IPv6      []string  `json:"ipv6"`
	SampledAt time.Time `json:"sampledAt"`
}

// historyLen is how many samples the window holds. At the default two-second
// cadence that is about four minutes, which is long enough to see a spike
// without making the payload heavy.
const historyLen = 120

// push appends to a ring-buffer-style slice, dropping the oldest value once the
// window is full.
func push[T any](buf []T, v T) []T {
	buf = append(buf, v)
	if len(buf) > historyLen {
		buf = buf[len(buf)-historyLen:]
	}
	return buf
}

// Collector samples the host on an interval.
type Collector struct {
	diskPath  string
	interval  time.Duration
	log       *slog.Logger
	startedAt time.Time
	proc      *process.Process

	mu   sync.RWMutex
	snap Snapshot
	hist History

	lastNetAt   time.Time
	lastSent    uint64
	lastRecv    uint64
	connsEvery  int
	connsTicks  int
	lastTCP     int
	lastUDP     int
	staticSetUp bool
}

// New builds a collector. diskPath selects the filesystem to report; it should
// be the data directory, because that is the one whose filling up stops the
// panel working.
func New(diskPath string, interval time.Duration, log *slog.Logger) *Collector {
	if interval < time.Second {
		interval = 2 * time.Second
	}
	c := &Collector{
		diskPath:  diskPath,
		interval:  interval,
		log:       log,
		startedAt: time.Now(),
		// Connection counting walks every open socket, which is the one
		// expensive call here, so it runs at a fraction of the main cadence.
		connsEvery: 5,
	}
	if p, err := process.NewProcess(int32(os.Getpid())); err == nil {
		c.proc = p
	}
	return c
}

// Start samples immediately and then on the interval until ctx is done.
func (c *Collector) Start(ctx context.Context) {
	c.sample(ctx)
	go func() {
		t := time.NewTicker(c.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.sample(ctx)
			}
		}
	}()
}

// Snapshot returns the most recent sample.
//
// A nil collector returns a zero snapshot rather than panicking. Telemetry is
// the least important thing this panel does, and a missing collector should
// cost an empty gauge, not the whole request.
func (c *Collector) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snap
}

func (c *Collector) sample(ctx context.Context) {
	var s Snapshot
	s.SampledAt = time.Now()

	// A failed probe leaves its section zeroed rather than aborting the sample:
	// a container without swap, or a platform without load averages, should
	// still get CPU and memory.
	if pcts, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(pcts) > 0 {
		s.CPU.Percent = pcts[0]
	}
	s.CPU.Cores = runtime.NumCPU()
	if infos, err := cpu.InfoWithContext(ctx); err == nil && len(infos) > 0 {
		s.CPU.Model = infos[0].ModelName
		if infos[0].Cores > 0 && len(infos) > 1 {
			s.CPU.Cores = len(infos) * int(infos[0].Cores)
		}
	}
	if avg, err := load.AvgWithContext(ctx); err == nil && avg != nil {
		s.CPU.Load1, s.CPU.Load5, s.CPU.Load15 = avg.Load1, avg.Load5, avg.Load15
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil && vm != nil {
		s.Memory = newUsage(vm.Used, vm.Total)
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil && sw != nil {
		s.Swap = newUsage(sw.Used, sw.Total)
	}
	if du, err := disk.UsageWithContext(ctx, c.diskPath); err == nil && du != nil {
		s.Disk = newUsage(du.Used, du.Total)
	}

	if hi, err := host.InfoWithContext(ctx); err == nil && hi != nil {
		s.Host = Host{
			OS:        hi.OS,
			Platform:  hi.Platform + " " + hi.PlatformVersion,
			Kernel:    hi.KernelVersion,
			Arch:      hi.KernelArch,
			Hostname:  hi.Hostname,
			UptimeSec: hi.Uptime,
		}
	}

	c.sampleNetwork(ctx, &s)

	s.Panel.Goroutines = runtime.NumGoroutine()
	s.Panel.UptimeSec = int64(time.Since(c.startedAt).Seconds())
	if c.proc != nil {
		if mi, err := c.proc.MemoryInfoWithContext(ctx); err == nil && mi != nil {
			s.Panel.MemoryBytes = mi.RSS
		}
		if n, err := c.proc.NumThreadsWithContext(ctx); err == nil {
			s.Panel.Threads = int(n)
		}
	}

	s.IPv4, s.IPv6 = localAddresses()

	c.mu.Lock()
	c.hist.CPU = push(c.hist.CPU, s.CPU.Percent)
	c.hist.Memory = push(c.hist.Memory, s.Memory.Percent)
	c.hist.Swap = push(c.hist.Swap, s.Swap.Percent)
	c.hist.Disk = push(c.hist.Disk, s.Disk.Percent)
	c.hist.Up = push(c.hist.Up, s.Network.SentRate)
	c.hist.Down = push(c.hist.Down, s.Network.RecvRate)
	c.hist.TCP = push(c.hist.TCP, s.Network.TCPConns)
	c.hist.UDP = push(c.hist.UDP, s.Network.UDPConns)
	s.History = c.hist
	c.snap = s
	c.mu.Unlock()
}

func (c *Collector) sampleNetwork(ctx context.Context, s *Snapshot) {
	counters, err := gnet.IOCountersWithContext(ctx, false)
	if err == nil && len(counters) > 0 {
		sent, recv := counters[0].BytesSent, counters[0].BytesRecv
		s.Network.BytesSent, s.Network.BytesRecv = sent, recv

		if !c.lastNetAt.IsZero() {
			elapsed := time.Since(c.lastNetAt).Seconds()
			// Counters wrap and reset; a value below the previous one means the
			// interface restarted, and reporting a negative rate as a huge
			// unsigned number would be worse than reporting nothing.
			if elapsed > 0 {
				if sent >= c.lastSent {
					s.Network.SentRate = uint64(float64(sent-c.lastSent) / elapsed)
				}
				if recv >= c.lastRecv {
					s.Network.RecvRate = uint64(float64(recv-c.lastRecv) / elapsed)
				}
			}
		}
		c.lastNetAt, c.lastSent, c.lastRecv = time.Now(), sent, recv
	}

	c.connsTicks++
	if c.connsTicks >= c.connsEvery || !c.staticSetUp {
		c.connsTicks = 0
		c.staticSetUp = true
		if conns, err := gnet.ConnectionsWithContext(ctx, "tcp"); err == nil {
			c.lastTCP = len(conns)
		}
		if conns, err := gnet.ConnectionsWithContext(ctx, "udp"); err == nil {
			c.lastUDP = len(conns)
		}
	}
	s.Network.TCPConns, s.Network.UDPConns = c.lastTCP, c.lastUDP
}

// localAddresses lists the machine's routable addresses, which is what an
// operator needs when telling a customer where to point a client.
// An empty Go slice marshals to JSON null, which every consumer would then have
// to guard against. Starting them non-nil means "no addresses" arrives as [].
func localAddresses() (v4, v6 []string) {
	v4, v6 = []string{}, []string{}

	ifaces, err := net.Interfaces()
	if err != nil {
		return v4, v6
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() {
				continue
			}
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				v4 = append(v4, ip4.String())
			} else {
				v6 = append(v6, ipnet.IP.String())
			}
		}
	}
	return v4, v6
}
