package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/upgradecheck"
)

// The panel's contract with what is built on it -- scripts against the API,
// and the apps customers point at their links -- as this release keeps it,
// byte for byte. A change to any answer here fails until the golden file is
// regenerated with -update, so a change is always one somebody chose:
//
//	go test ./cmd/wui -run TestContract -update
//
// The data is the newest fixture's, so keys, tokens and dates are fixed and
// the answers are the same on every run. What still moves on its own -- the
// clock, a nonce -- is masked.

var updateGolden = flag.Bool("update", false, "rewrite the contract golden files")

const contractDir = "testdata/contract"

// contractFixture is the release whose database the contract is read from.
const contractFixture = "v2.6.0"

// volatile are fields whose value moves with the clock or the machine, not
// with the data; their presence and type are still part of the contract.
var volatile = map[string]bool{
	"lastHandshake": true, "lastSeenAt": true, "lastEndpoint": true,
	"onlineNow": true, "speed": true, "running": true, "version": true,
	"taken": true, "checkedAt": true, "now": true, "serverTime": true,
	// A row made during the test is stamped with the moment it was made.
	"createdAt": true, "updatedAt": true,
}

func TestContract(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a panel")
	}
	dir := filepath.Join(fixtures, contractFixture)
	m, err := upgradecheck.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	copyFile(t, filepath.Join(dir, "wui.db"), filepath.Join(data, "wui.db"))
	// Times are shown in the panel's zone: UTC here, so the answers are the
	// same on a laptop in Tehran and on CI.
	if err := sqliteFile(t, filepath.Join(data, "wui.db")).
		Exec(`INSERT INTO settings (key, value) VALUES ('panel.timeLocation', 'UTC')
		      ON CONFLICT (key) DO UPDATE SET value = 'UTC'`).Error; err != nil {
		t.Fatal(err)
	}
	listen := listenOf(t, m)
	p := startPanel(t, panelEnv(data, listen), listen)
	ctx := context.Background()
	c, err := upgradecheck.Login(ctx, p.base, m.Admin.Username, m.Admin.Password)
	if err != nil {
		t.Fatal(err)
	}

	// The API. Reads first; the calls that change something last, in the
	// order that keeps each answer the same from run to run.
	api := []struct {
		name, method, path string
		body               any
	}{
		{"clients-list", "GET", "api/clients?perPage=50", nil},
		{"client", "GET", "api/clients/1", nil},
		{"client-ids", "GET", "api/clients/ids", nil},
		{"client-ids-filtered", "GET", "api/clients/ids?status=disabled", nil},
		{"interfaces", "GET", "api/interfaces", nil},
		{"settings", "GET", "api/settings", nil},
		{"subscription-settings", "GET", "api/subscription", nil},
		{"client-subscription", "GET", "api/clients/1/subscription", nil},
		{"extend-preview-time", "POST", "api/clients/extend",
			map[string]any{"ids": []int{1, 2, 3, 5}, "kind": "time", "days": 2, "dryRun": true}},
		{"extend-preview-traffic-back", "POST", "api/clients/extend",
			map[string]any{"ids": []int{1, 2, 3, 4}, "kind": "traffic", "gb": 1, "subtract": true, "dryRun": true}},
		{"extend-nothing-selected", "POST", "api/clients/extend",
			map[string]any{"ids": []int{}, "kind": "time", "days": 1, "dryRun": true}},
		{"reseller-create", "POST", "api/admins",
			map[string]any{"username": "contract-reseller", "password": "contract-pass-1", "role": "reseller", "clientLimit": 5}},
		{"admins-bulk-extend", "POST", "api/admins/bulk",
			map[string]any{"action": "setClientLimit", "ids": []int{2}, "clientLimit": 9}},
		{"admins-bulk-unknown", "POST", "api/admins/bulk",
			map[string]any{"action": "fly", "ids": []int{2}}},
		{"not-found", "GET", "api/clients/999", nil},
	}
	for _, a := range api {
		status, raw := c.Raw(ctx, a.method, a.path, a.body)
		golden(t, a.name+".json", contractJSON(t, status, raw))
	}

	// The subscription service, as a customer's app reads it: status, the
	// headers apps read, and the body itself.
	for _, cu := range m.Clients {
		got, err := upgradecheck.FetchSubscription(ctx, cu.SubLink)
		if err != nil {
			t.Fatal(err)
		}
		golden(t, "sub/"+cu.Name+".txt", subscriptionAnswer(got))
	}
	first := m.Clients[0]
	for _, q := range []string{"?format=base64", "?format=conf"} {
		got, err := upgradecheck.FetchSubscription(ctx, first.SubLink+q)
		if err != nil {
			t.Fatal(err)
		}
		golden(t, "sub/"+first.Name+strings.ReplaceAll(q, "?format=", ".")+".txt", subscriptionAnswer(got))
	}
	// A link that never existed, and the customer's page a browser gets.
	missing, err := upgradecheck.FetchSubscription(ctx, first.SubLink[:strings.LastIndex(first.SubLink, "/")+1]+"no-such-token")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "sub/unknown-token.txt", subscriptionAnswer(missing))
	page := fetchPage(t, first.SubLink)
	golden(t, "sub/page.html", page)

	// Last, because it issues new keys: what a rotation answers.
	status, raw := c.Raw(ctx, "POST", "api/clients/2/rotate-keys", map[string]any{})
	golden(t, "rotate-keys.json", contractJSON(t, status, raw))
}

// contractJSON is an API answer as it is kept: the status, and the body with
// its keys in order and the volatile fields masked.
func contractJSON(t *testing.T, status int, raw []byte) []byte {
	t.Helper()
	var body any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			body = strings.TrimSpace(string(raw))
		}
	}
	out, err := json.MarshalIndent(map[string]any{"status": status, "body": mask(body)}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

// mask replaces volatile values, and secrets the panel generates fresh (the
// keys a rotation issues), with their kind.
func mask(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if volatile[k] && val != nil {
				x[k] = fmt.Sprintf("<%T>", val)
				continue
			}
			x[k] = mask(val)
		}
	case []any:
		for i := range x {
			x[i] = mask(x[i])
		}
	}
	return v
}

// subscriptionAnswer is what an app reads from a link, as text.
func subscriptionAnswer(f *upgradecheck.Fetched) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "status: %d\n", f.Status)
	keys := []string{"Content-Type", "Content-Disposition", "Subscription-Userinfo",
		"Profile-Update-Interval", "Profile-Title", "Cache-Control", "Retry-After"}
	for _, k := range keys {
		if v := f.Header.Get(k); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	b.WriteString("\n")
	b.Write(f.Body)
	return b.Bytes()
}

var (
	nonce   = regexp.MustCompile(`nonce="[^"]*"|'nonce-[^']*'`)
	pngData = regexp.MustCompile(`data:image/png;base64,[A-Za-z0-9+/=]+`)
	// The time left on a plan, counted from now: "1552d", "12 days left".
	timeLeft = regexp.MustCompile(`([>\s])\d+(d|h|m| days left| hours left| minutes left)([<\s])`)
)

// fetchPage is the customer's page as a browser gets it, nonces masked.
func fetchPage(t *testing.T, link string) []byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, link, nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "Mozilla/5.0 (contract test)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the customer's page: %s", resp.Status)
	}
	raw = nonce.ReplaceAll(raw, []byte("nonce=<masked>"))
	// The QR codes are PNGs, and how a PNG is compressed differs between Go
	// releases; what they encode -- the links -- is in the page as text.
	raw = pngData.ReplaceAll(raw, []byte("data:image/png;base64,<png>"))
	return timeLeft.ReplaceAll(raw, []byte("${1}<left>${3}"))
}

// golden compares got with the file of that name, or writes it with -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join(contractDir, name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("%s: no golden file (run with -update to create it): %v", name, err)
		return
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s changed:\n%s", name, firstDifference(want, got))
	}
}

// firstDifference shows where two texts part, with a little around it.
func firstDifference(want, got []byte) string {
	a, b := strings.Split(string(want), "\n"), strings.Split(string(got), "\n")
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return fmt.Sprintf("line %d\n  want: %.200s\n  got:  %.200s", i+1, x, y)
		}
	}
	return "(same lines, different line endings)"
}
