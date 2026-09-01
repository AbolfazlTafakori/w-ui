package ovpndriver

import (
	"net/netip"
	"testing"

	"github.com/abolfazl/w-ui/internal/backend"
)

func acc(id uint, username, secret, ip string) backend.DesiredAccount {
	return backend.DesiredAccount{
		ID: id, Username: username, Secret: secret, IP: netip.MustParseAddr(ip),
	}
}

func TestNewAccountIsWritten(t *testing.T) {
	d := computeDiff(toAccountSet([]backend.DesiredAccount{acc(1, "u1", "s1", "10.67.0.2")}),
		map[string]string{})

	if len(d.write) != 1 || d.write[0].Username != "u1" {
		t.Fatalf("write = %+v, want the one new account", d.write)
	}
	if len(d.remove) != 0 {
		t.Error("nothing should be removed on an empty server")
	}
}

func TestUnknownAccountIsRemoved(t *testing.T) {
	// A username the server still holds but the database no longer lists
	// belongs to a deleted or cut-off customer.
	d := computeDiff(accountSet{}, map[string]string{"stale": "10.67.0.9"})
	if len(d.remove) != 1 || d.remove[0] != "stale" {
		t.Fatalf("remove = %v, want the stale account", d.remove)
	}
}

func TestUnchangedAccountIsLeftAlone(t *testing.T) {
	want := toAccountSet([]backend.DesiredAccount{acc(1, "u1", "s1", "10.67.0.2")})
	d := computeDiff(want, map[string]string{"u1": "10.67.0.2"})

	if len(d.write)+len(d.remove) != 0 {
		t.Errorf("a matching account produced work: %+v", d)
	}
	if d.report(1, 0).Changed() {
		t.Error("an unchanged server should not report a change")
	}
}

func TestMovedAddressIsRewritten(t *testing.T) {
	// If the pinned address does not follow, the customer keeps the old one and
	// the quota attached to the new one never counts them.
	want := toAccountSet([]backend.DesiredAccount{acc(1, "u1", "s1", "10.67.0.5")})
	d := computeDiff(want, map[string]string{"u1": "10.67.0.2"})

	if len(d.write) != 1 || d.write[0].IP != "10.67.0.5" {
		t.Fatalf("write = %+v, want the new address", d.write)
	}
	if len(d.remove) != 0 {
		t.Error("moving an address should not remove the account")
	}
}

func TestAccountWithoutCredentialsIsSkipped(t *testing.T) {
	// A WireGuard account has neither. A blank field would write a credential
	// line that an empty login could match.
	set := toAccountSet([]backend.DesiredAccount{
		acc(1, "", "s", "10.67.0.2"),
		acc(2, "u", "", "10.67.0.3"),
		acc(3, "real", "ok", "10.67.0.4"),
	})
	if len(set) != 1 {
		t.Fatalf("account set has %d entries, want only the complete one", len(set))
	}
	if _, ok := set["real"]; !ok {
		t.Error("the complete account was dropped")
	}
}

func TestReportCountsEveryCategory(t *testing.T) {
	want := toAccountSet([]backend.DesiredAccount{
		acc(1, "keep", "s", "10.67.0.2"),
		acc(2, "move", "s", "10.67.0.9"),
		acc(3, "new", "s", "10.67.0.4"),
	})
	have := map[string]string{
		"keep":  "10.67.0.2",
		"move":  "10.67.0.3",
		"stale": "10.67.0.8",
	}
	d := computeDiff(want, have)

	added := 0
	for _, a := range d.write {
		if _, existed := have[a.Username]; !existed {
			added++
		}
	}
	r := d.report(len(want)-len(d.write), added)

	if r.Added != 1 || r.Updated != 1 || r.Removed != 1 || r.Unchanged != 1 {
		t.Errorf("report = %+v, want 1 of each", r)
	}
}

func TestStatusIsParsedIntoSessions(t *testing.T) {
	raw := `TITLE,OpenVPN 2.6.19
TIME,2026-09-01 12:00:00,1788264000
HEADER,CLIENT_LIST,Common Name,Real Address,Virtual Address,Virtual IPv6 Address,Bytes Received,Bytes Sent,Connected Since,Connected Since (time_t),Username,Client ID,Peer ID,Data Channel Cipher
CLIENT_LIST,alpha,203.0.113.7:51820,10.67.0.2,,1048576,2097152,2026-09-01 11:00:00,1788260400,alpha,0,0,AES-256-GCM
CLIENT_LIST,bravo,198.51.100.4:44311,10.67.0.3,,512,1024,2026-09-01 11:30:00,1788262200,bravo,1,1,AES-256-GCM
GLOBAL_STATS,Max bcast/mcast queue length,0
END`

	got := parseStatus(raw)
	if len(got) != 2 {
		t.Fatalf("parsed %d sessions, want 2", len(got))
	}

	if got[0].Username != "alpha" || got[0].RX != 1048576 || got[0].TX != 2097152 {
		t.Errorf("first session = %+v", got[0])
	}
	// The public source address is what the sharing detector watches.
	if got[0].RealIP != "203.0.113.7:51820" {
		t.Errorf("real address = %q", got[0].RealIP)
	}
	if got[0].Since != 1788260400 {
		t.Errorf("connected since = %d", got[0].Since)
	}
	if got[1].Username != "bravo" {
		t.Errorf("second session = %+v", got[1])
	}
}

func TestStatusIgnoresEverythingThatIsNotAClient(t *testing.T) {
	raw := `TITLE,OpenVPN 2.6.19
ROUTING_TABLE,10.67.0.2,alpha,203.0.113.7:51820,1788264000
GLOBAL_STATS,Max bcast/mcast queue length,0
END`
	if got := parseStatus(raw); len(got) != 0 {
		t.Errorf("parsed %d sessions from a status with no clients: %+v", len(got), got)
	}
}

func TestTruncatedStatusLineIsSkippedRatherThanCounted(t *testing.T) {
	// A short line means the format was not what we expected. Treating it as a
	// client with zero traffic would look like an idle customer rather than a
	// parse failure, and would hide the problem.
	raw := "CLIENT_LIST,alpha,203.0.113.7:51820\nEND"
	if got := parseStatus(raw); len(got) != 0 {
		t.Errorf("a truncated line produced %+v", got)
	}
}

func TestUnauthenticatedSessionIsNotReported(t *testing.T) {
	raw := "CLIENT_LIST,UNDEF,203.0.113.7:51820,,,0,0,2026-09-01 11:00:00,1788260400\nEND"
	if got := parseStatus(raw); len(got) != 0 {
		t.Errorf("an unauthenticated session was reported as a client: %+v", got)
	}
}

func TestPinnedAddressIsReadBackFromTheDirectory(t *testing.T) {
	got := parseClientConfigs(map[string]string{
		"alpha": "ifconfig-push 10.67.0.2 255.255.0.0\n",
		"bravo": "# a comment\nifconfig-push 10.67.0.3 255.255.0.0\n",
		"empty": "# nothing here\n",
	})

	if got["alpha"] != "10.67.0.2" || got["bravo"] != "10.67.0.3" {
		t.Errorf("parsed assignments = %v", got)
	}
	// A file with no assignment must not register as an account at an unknown
	// address, or the diff would think it was already correct.
	if _, ok := got["empty"]; ok {
		t.Error("a file with no assignment was treated as one")
	}
}

func TestKillCommandRefusesANameThatCouldCarryASecondCommand(t *testing.T) {
	// The management protocol is line-based. A username containing a newline
	// would let whatever follows it run as a command of its own.
	for _, bad := range []string{"", "alpha\nkill bravo", "alpha\r\nquit"} {
		if _, err := killCommand(bad); err == nil {
			t.Errorf("killCommand(%q) was accepted", bad)
		}
	}

	got, err := killCommand("alpha")
	if err != nil {
		t.Fatalf("killCommand: %v", err)
	}
	if got != "kill alpha\n" {
		t.Errorf("killCommand = %q", got)
	}
}
