package agentkit

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

func TestGatewayIsTheSubnetsFirstHost(t *testing.T) {
	gw, err := Gateway(" 10.77.0.9/24 ")
	if err != nil || gw.String() != "10.77.0.1" {
		t.Fatalf("Gateway = %v, %v", gw, err)
	}
	for _, bad := range []string{"", "fd00::/64", "not a subnet"} {
		if _, err := Gateway(bad); err == nil {
			t.Errorf("Gateway(%q) took it", bad)
		}
	}
}

func TestResolversAreTwoIPv4AtMost(t *testing.T) {
	got := strings.Join(Resolvers("1.1.1.1, ::1, junk, 9.9.9.9, 8.8.8.8"), ",")
	if got != "1.1.1.1,9.9.9.9" {
		t.Fatalf("Resolvers = %s", got)
	}
}

func TestWriteAtomicLeavesOnlyTheFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deep")
	path := filepath.Join(dir, "state.json")
	for _, body := range []string{"one", "two"} {
		if err := WriteAtomic(path, []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(path); string(b) != "two" {
		t.Fatalf("file = %q", b)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("left behind: %v", ents)
	}
}

func TestAStaleReportIsNoAgent(t *testing.T) {
	p := l2tpconf.PathsFor(t.TempDir(), "l2tp")
	if _, err := FreshReport(p); err == nil {
		t.Fatal("no report taken as fresh")
	}
	write := func(at time.Time) {
		raw, _ := json.Marshal(l2tpconf.Report{At: at.Unix()})
		if err := WriteAtomic(p.Report(), raw, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	write(time.Now())
	if _, err := FreshReport(p); err != nil {
		t.Fatalf("a report just written: %v", err)
	}
	write(time.Now().Add(-ReportStale - time.Second))
	if _, err := FreshReport(p); !errors.Is(err, ErrNoAgent) {
		t.Fatalf("an old report: %v", err)
	}
}

func TestServersAreTheTunnelsOwnFirstThenItsHosts(t *testing.T) {
	iface := &model.Interface{EndpointHost: "vpn.example.com", Hosts: []model.Host{
		{Address: "b.example.com", Priority: 2, Enabled: true},
		{Address: "a.example.com", Priority: 1, Enabled: true},
		{Address: "off.example.com", Priority: 0, Enabled: false},
		{Address: "vpn.example.com", Priority: 3, Enabled: true},
	}}
	if got := strings.Join(Servers(iface), ","); got != "vpn.example.com,a.example.com,b.example.com" {
		t.Fatalf("Servers = %s", got)
	}
}

func TestServerIPv4ResolvesNamesOnlyAndRemembers(t *testing.T) {
	asked := 0
	saved := lookupIPv4
	lookupIPv4 = func(_ context.Context, host string) ([]netip.Addr, error) {
		asked++
		if host == "vpn.example.test" {
			return []netip.Addr{netip.MustParseAddr("203.0.113.9"), netip.MustParseAddr("203.0.113.7")}, nil
		}
		return nil, errors.New("no such host")
	}
	t.Cleanup(func() { lookupIPv4 = saved })
	if ips := ServerIPv4("198.51.100.1"); ips != nil {
		t.Fatalf("an address resolved: %v", ips)
	}
	for range 2 {
		if got := strings.Join(ServerIPv4("vpn.example.test"), ","); got != "203.0.113.7,203.0.113.9" {
			t.Fatalf("ServerIPv4 = %s", got)
		}
	}
	if asked != 1 {
		t.Fatalf("asked DNS %d times; the answer is remembered", asked)
	}
	if ips := ServerIPv4("nowhere.example.test"); len(ips) != 0 {
		t.Fatalf("a name that does not resolve: %v", ips)
	}
}
