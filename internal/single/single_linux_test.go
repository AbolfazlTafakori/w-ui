//go:build linux

package single

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// These tests claim a name of their own. The real one belongs to whatever
// panel is running in this network namespace -- on CI, the panels the
// upgrade tests start, in another package, at the same time.
func TestMain(m *testing.M) {
	if socketName != "@wui-panel" {
		fmt.Fprintln(os.Stderr, "the panel's claim is not @wui-panel; two releases would not see each other")
		os.Exit(1)
	}
	socketName = fmt.Sprintf("@wui-panel-test-%d", os.Getpid())
	os.Exit(m.Run())
}

// The claim is what stands between a server and two panels quietly undoing each
// other's firewall rules. If it ever stops refusing, nothing else does.
func TestASecondPanelIsRefused(t *testing.T) {
	first, err := Claim("pid 1, data /var/lib/wui, listening on 127.0.0.1:2053")
	if err != nil {
		t.Fatalf("the first panel could not claim the machine: %v", err)
	}
	defer first.Release()

	second, err := Claim("pid 2, data /var/lib/wui-two, listening on 127.0.0.1:9999")
	if err == nil {
		second.Release()
		t.Fatal("a second panel was allowed to start on the same machine")
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("the refusal was not recognisable as one: %v", err)
	}
}

// The refusal has to say which panel is in the way. An operator reading
// "address already in use" in a unit that will not start has been told nothing.
func TestTheRefusalNamesTheOneAlreadyRunning(t *testing.T) {
	first, err := Claim("pid 4242, data /var/lib/wui, listening on 127.0.0.1:2053")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	defer first.Release()

	_, err = Claim("pid 9, data /tmp/other, listening on 127.0.0.1:9999")
	if err == nil {
		t.Fatal("the second panel was not refused")
	}
	for _, want := range []string{"pid 4242", "/var/lib/wui", "127.0.0.1:2053"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// Releasing has to actually let the next one in, or an upgrade would leave the
// machine claimed by a process that has gone.
func TestReleasingLetsTheNextPanelStart(t *testing.T) {
	first, err := Claim("pid 1, data /var/lib/wui, listening on 127.0.0.1:2053")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	first.Release()

	second, err := Claim("pid 2, data /var/lib/wui, listening on 127.0.0.1:2053")
	if err != nil {
		t.Fatalf("the machine stayed claimed after the holder released it: %v", err)
	}
	second.Release()
}

// The message has to explain the consequence, not just the fact. "Already
// running" invites an operator to kill the other one and try again on a server
// where that is exactly the wrong move.
func TestTheRefusalSaysWhyItMatters(t *testing.T) {
	err := Describe(ErrAlreadyRunning, "pid 1")
	if !strings.Contains(err.Error(), "firewall rules") {
		t.Errorf("the refusal does not say what goes wrong: %v", err)
	}
	if !strings.Contains(Describe(ErrAlreadyRunning, "").Error(), "separate machine") {
		t.Errorf("the refusal with no holder does not say what to do instead")
	}
}
