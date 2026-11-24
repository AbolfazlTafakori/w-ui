package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// done is closed when the process has ended.
	done chan struct{}
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
//
// A fixture's panel listens where it did when it was made, and its links
// name that address. The port is in the range the system hands out for
// connections, so another test may hold it for a moment: the start waits
// for it to come free, and a panel that still lost it to someone between
// the check and its own listen is started again.
func startPanel(t *testing.T, env []string, listen string) *panel {
	t.Helper()
	for attempt := 1; ; attempt++ {
		waitFree(t, listen)
		p := launch(t, env, listen)
		err := p.waitUp(90 * time.Second)
		if err == nil {
			return p
		}
		out := p.output()
		p.stop()
		if attempt < 4 && strings.Contains(out, "address already in use") {
			t.Logf("%s was taken as the panel started; starting it again", listen)
			continue
		}
		t.Fatalf("%v\n%s", err, out)
	}
}

// launch starts the panel process.
func launch(t *testing.T, env []string, listen string) *panel {
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
	p := &panel{t: t, cmd: cmd, log: logFile, base: "http://" + listen + "/", done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(p.stop)
	return p
}

// waitUp waits for the panel to answer, and gives up at once if it ends.
func (p *panel) waitUp(within time.Duration) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-p.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	err := upgradecheck.WaitUp(ctx, p.base, within)
	select {
	case <-p.done:
		return fmt.Errorf("the panel at %s ended before it answered", p.base)
	default:
	}
	return err
}

// stop ends the panel; safe to call twice.
func (p *panel) stop() {
	select {
	case <-p.done:
	default:
		_ = p.cmd.Process.Kill()
		<-p.done
	}
	if p.log != nil {
		p.log.Close()
	}
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

// waitFree waits, up to a minute, until the address can be listened on.
func waitFree(t *testing.T, listen string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		l, err := net.Listen("tcp", listen)
		if err == nil {
			l.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s stayed taken: %v", listen, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
