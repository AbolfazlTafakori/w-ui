package pptpd

import (
	"os"
	"path/filepath"
	"testing"
)

// The file the panel writes is read as the server's configuration, and one
// that names no pppd options is refused rather than run without them.
func TestTheTunnelsFileIsTheServersConfiguration(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "pptpd.conf")
	if err := os.WriteFile(conf, []byte("option /run/wui-l2tp/pptp-options\ndelegate\nconnections 300\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := ReadConfig(conf)
	if err != nil {
		t.Fatal(err)
	}
	if c.Options != "/run/wui-l2tp/pptp-options" || c.MaxConns != 300 || c.Listen != ":1723" {
		t.Fatalf("%+v", c)
	}
	if err := os.WriteFile(conf, []byte("delegate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConfig(conf); err == nil {
		t.Fatal("a configuration without pppd's options was taken")
	}
}
