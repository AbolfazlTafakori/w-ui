package api

import (
	"strings"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/reconciler"
)

// The panel is unhealthy when the loop stops or usage cannot be written, and
// healthy otherwise -- including one slow pass.
func TestHealthIsTheLoopAndTheUsageWrites(t *testing.T) {
	now := time.Now()
	ok := reconciler.Health{Every: 2 * time.Second, Ticks: 10, LastRun: now.Add(-3 * time.Second)}
	if p := healthProblems(ok, now); len(p) != 0 {
		t.Fatalf("a running loop is unhealthy: %v", p)
	}
	stuck := ok
	stuck.LastRun = now.Add(-5 * time.Minute)
	if p := healthProblems(stuck, now); len(p) != 1 || !strings.Contains(p[0], "last ran") {
		t.Fatalf("a stopped loop: %v", p)
	}
	failing := ok
	failing.UsageFailing = true
	if p := healthProblems(failing, now); len(p) != 1 || !strings.Contains(p[0], "usage") {
		t.Fatalf("usage not written: %v", p)
	}
}

func TestMetricsAreInPrometheusText(t *testing.T) {
	var m metrics
	m.family("wui_tunnel_up", "gauge", "Whether a tunnel is running.")
	m.value("wui_tunnel_up", 1, "tunnel", `wg"0`, "protocol", "wireguard")
	want := "# HELP wui_tunnel_up Whether a tunnel is running.\n# TYPE wui_tunnel_up gauge\n" +
		`wui_tunnel_up{tunnel="wg\"0",protocol="wireguard"} 1` + "\n"
	if got := m.b.String(); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
