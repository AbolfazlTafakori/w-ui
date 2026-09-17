//go:build linux

package routing

import (
	"strings"
	"testing"
)

// An OpenVPN outbound is a tunnel, not a place to run things: a profile
// that names a script, a plugin, a file to include or a path to read has
// those lines dropped, however they are spelled.
func TestOpenVPNProfileCannotRunOrRead(t *testing.T) {
	in := strings.Join([]string{
		"client",
		"remote vpn.example.com 1194",
		"up /tmp/evil.sh",
		"\tdown\t/tmp/evil.sh",
		"--plugin /usr/lib/openvpn/plugins/x.so",
		"route-up  /bin/sh",
		"script-security 2",
		"config /etc/passwd",
		"ca /etc/ssl/private/key.pem",
		"<ca>",
		"-----BEGIN CERTIFICATE-----",
		"</ca>",
		"<up>",
		"rm -rf /",
		"</up>",
		"setenv EVIL 1",
		"cipher AES-256-GCM",
	}, "\n")
	got := cleanOpenVPNProfile(in)
	for _, kept := range []string{"client", "remote vpn.example.com 1194", "<ca>", "-----BEGIN CERTIFICATE-----", "</ca>", "cipher AES-256-GCM"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q was dropped", kept)
		}
	}
	for _, gone := range []string{"evil.sh", "plugin", "route-up", "script-security", "/etc/passwd", "key.pem", "<up>", "rm -rf", "setenv"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived:\n%s", gone, got)
		}
	}
}
