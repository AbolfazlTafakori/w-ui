package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/upgradecheck"
)

// The tests here start the panel itself -- the whole of main, as a server
// runs it -- by running this test binary again with asPanel set. A test then
// talks to it the way the operator's browser and the customers' apps do.
const asPanel = "WUI_TEST_AS_PANEL"

func TestMain(m *testing.M) {
	if os.Getenv(asPanel) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// panel is one running panel.
type panel struct {
	t    *testing.T
	cmd  *exec.Cmd
	log  *os.File
	base string
}

// panelEnv is the environment a panel on dataDir runs with.
func panelEnv(dataDir, listen string, extra ...string) []string {
	env := append(os.Environ(),
		asPanel+"=1",
		"WUI_DATA_DIR="+dataDir,
		"WUI_BACKUP_DIR="+filepath.Join(dataDir, "backups"),
		"WUI_LISTEN="+listen,
		"WUI_LOG_LEVEL=warn",
	)
	return append(env, extra...)
}

// startPanel runs the panel with env and waits for it to answer.
func startPanel(t *testing.T, env []string, listen string) *panel {
	t.Helper()
	logFile, err := os.CreateTemp(t.TempDir(), "panel-*.log")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &panel{t: t, cmd: cmd, log: logFile, base: "http://" + listen + "/"}
	t.Cleanup(p.stop)
	if err := upgradecheck.WaitUp(context.Background(), p.base, 90*time.Second); err != nil {
		p.stop()
		t.Fatalf("%v\n%s", err, p.output())
	}
	return p
}

// stop ends the panel; safe to call twice.
func (p *panel) stop() {
	if p.cmd.Process == nil || p.cmd.ProcessState != nil {
		return
	}
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
	p.log.Close()
	// Windows keeps a file open a moment after its process ends; a data
	// directory removed straight away would fail the test's cleanup.
	time.Sleep(200 * time.Millisecond)
}

// output is what the panel has logged, for a failure message.
func (p *panel) output() string {
	raw, _ := os.ReadFile(p.log.Name())
	if len(raw) > 6000 {
		raw = raw[len(raw)-6000:]
	}
	return string(raw)
}

// runCLI runs a wui subcommand, as an operator does in a terminal.
func runCLI(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// copyFile copies src to dst.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
}
