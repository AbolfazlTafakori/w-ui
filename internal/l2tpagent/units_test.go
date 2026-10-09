package l2tpagent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// heredoc is the text install.sh writes for a unit, with its variables
// filled in the way the shell would.
func heredoc(t *testing.T, script, unit, binary, dataDir string) string {
	t.Helper()
	open := "cat >/etc/systemd/system/" + unit + " <<HELPER\n"
	i := strings.Index(script, open)
	if i < 0 {
		t.Fatalf("install.sh does not write %s", unit)
	}
	body := script[i+len(open):]
	j := strings.Index(body, "\nHELPER\n")
	if j < 0 {
		t.Fatalf("the %s heredoc does not end", unit)
	}
	body = body[:j+1]
	return strings.NewReplacer("$BIN_PATH", binary, "$DATA_DIR", dataDir).Replace(body)
}

// The installer and this build write the same units: a server updated from
// the panel's button and one updated by the script end up the same.
func TestTheUnitsAreTheInstallersOwn(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(string(raw), "\r\n", "\n")
	service, watch, err := Units("/usr/local/bin/wui", "/var/lib/wui")
	if err != nil {
		t.Fatal(err)
	}
	if want := heredoc(t, script, ServiceUnit, "/usr/local/bin/wui", "/var/lib/wui"); service != want {
		t.Errorf("the service differs from the installer's:\n--- here\n%s\n--- install.sh\n%s", service, want)
	}
	if want := heredoc(t, script, PathUnit, "/usr/local/bin/wui", "/var/lib/wui"); watch != want {
		t.Errorf("the path unit differs from the installer's:\n--- here\n%s\n--- install.sh\n%s", watch, want)
	}
	if !strings.Contains(service, "ExecStart=/usr/local/bin/wui l2tp-agent /var/lib/wui\n") ||
		!strings.Contains(watch, "PathExists=/var/lib/wui/l2tp/state.json\n") {
		t.Errorf("the units do not name the binary and the state:\n%s\n%s", service, watch)
	}
}

// A path that would end the line, split the command or be read by systemd
// as a specifier never reaches a unit.
func TestUnitsRefuseAPathTheyCannotCarry(t *testing.T) {
	for _, p := range []string{"relative/wui", "/usr/local/bin/w ui", "/var/lib/wui\nExecStartPre=/bin/sh", "/var/%h/wui", ""} {
		if _, _, err := Units(p, "/var/lib/wui"); !errors.Is(err, ErrUnitPath) {
			t.Errorf("binary %q: %v", p, err)
		}
		if _, _, err := Units("/usr/local/bin/wui", p); !errors.Is(err, ErrUnitPath) {
			t.Errorf("data directory %q: %v", p, err)
		}
	}
}

func TestUnitsInPlace(t *testing.T) {
	saved := UnitDir
	UnitDir = t.TempDir()
	t.Cleanup(func() { UnitDir = saved })
	if UnitsInPlace("/usr/local/bin/wui", "/var/lib/wui") {
		t.Fatal("missing units were reported in place")
	}
	service, watch, _ := Units("/usr/local/bin/wui", "/var/lib/wui")
	os.WriteFile(filepath.Join(UnitDir, ServiceUnit), []byte(service), 0o644)
	os.WriteFile(filepath.Join(UnitDir, PathUnit), []byte(watch+"# edited\n"), 0o644)
	if UnitsInPlace("/usr/local/bin/wui", "/var/lib/wui") {
		t.Fatal("a unit that differs was reported in place")
	}
	os.WriteFile(filepath.Join(UnitDir, PathUnit), []byte(watch), 0o644)
	if !UnitsInPlace("/usr/local/bin/wui", "/var/lib/wui") {
		t.Fatal("the units as rendered were not reported in place")
	}
}
