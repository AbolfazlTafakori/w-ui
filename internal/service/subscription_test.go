package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/backend"
	"github.com/abolfazl/w-ui/internal/database/model"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeDriver renders a recognisable configuration without needing a kernel.
type fakeDriver struct {
	body string
	err  error
}

func (f *fakeDriver) Protocol() model.Protocol                     { return model.ProtocolWireGuard }
func (f *fakeDriver) Open(context.Context, *model.Interface) error { return nil }
func (f *fakeDriver) Sync(context.Context, []backend.DesiredAccount) (backend.SyncReport, error) {
	return backend.SyncReport{}, nil
}
func (f *fakeDriver) Stats(context.Context) ([]backend.Stat, error) { return nil, nil }
func (f *fakeDriver) Kick(context.Context, uint) error              { return nil }
func (f *fakeDriver) Close() error                                  { return nil }
func (f *fakeDriver) Health(context.Context) error                  { return nil }
func (f *fakeDriver) Render(
	_ context.Context, acc *model.Account, _ *model.Interface,
) (backend.ClientProfile, error) {
	if f.err != nil {
		return backend.ClientProfile{}, f.err
	}
	return backend.ClientProfile{
		Filename: "x.conf",
		MIMEType: "text/plain",
		Body:     []byte(fmt.Sprintf("%s device=%d", f.body, acc.ID)),
	}, nil
}

// seed builds a customer with n devices on one interface.
func seed(t *testing.T, db *gorm.DB, n int) *model.Client {
	t.Helper()

	iface := model.Interface{
		Name: "wg0", Protocol: model.ProtocolWireGuard, Enabled: true,
		ListenPort: 51820, Subnet: "10.66.0.0/16", EndpointHost: "vpn.example.com",
		MTU: 1420,
	}
	if err := db.Create(&iface).Error; err != nil {
		t.Fatal(err)
	}

	expires := time.Now().Add(48 * time.Hour).UTC()
	c := model.Client{
		Name: "Roya", Protocol: model.ProtocolWireGuard, Status: model.StatusActive,
		QuotaBytes: 50 << 30, UpBytes: 1 << 30, DownBytes: 2 << 30,
		ExpiresAt: &expires, DeviceLimit: n,
	}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		acc := model.Account{
			ClientID: c.ID, InterfaceID: iface.ID,
			DeviceName: fmt.Sprintf("device-%d", i+1),
			IP:         fmt.Sprintf("10.66.0.%d", i+2),
		}
		if err := db.Create(&acc).Error; err != nil {
			t.Fatal(err)
		}
	}
	return &c
}

func newSubs(db *gorm.DB, drv backend.Backend) *Subscriptions {
	backends := map[uint]backend.Backend{}
	if drv != nil {
		backends[1] = drv
	}
	return NewSubscriptions(db, backends, NewHosts(db, quietLog()), quietLog())
}

// ── what a customer's app actually receives ──────────────────────────────────

func TestEveryDeviceIsInTheBundle(t *testing.T) {
	db := testDB(t)
	c := seed(t, db, 3)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})

	token, err := s.EnsureToken(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Serve(context.Background(), token, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b.Body), "CONFIG"); n != 3 {
		t.Fatalf("a customer with three devices got %d configurations", n)
	}
}

func TestQuotaAndExpiryReachTheCustomersApp(t *testing.T) {
	// This header is the only reason a customer can see their own remaining
	// data without asking. Getting the shape wrong means their app shows
	// nothing and they ask anyway.
	db := testDB(t)
	c := seed(t, db, 1)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})

	token, _ := s.EnsureToken(context.Background(), c.ID)
	b, err := s.Serve(context.Background(), token, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"upload=", "download=", "total=", "expire="} {
		if !strings.Contains(b.UserInfo, want) {
			t.Errorf("the usage header is missing %q: %s", want, b.UserInfo)
		}
	}
	if strings.Contains(b.UserInfo, "expire=0") {
		t.Error("a client with an expiry was reported as never expiring")
	}
}

func TestAnUnlimitedClientIsReportedAsUnlimitedRatherThanAsZeroLeft(t *testing.T) {
	db := testDB(t)
	c := seed(t, db, 1)
	// Unlimited and no expiry, which the header spells as zero for both.
	db.Model(c).Updates(map[string]any{"quota_bytes": 0, "expires_at": nil})

	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	token, _ := s.EnsureToken(context.Background(), c.ID)
	b, _ := s.Serve(context.Background(), token, "")

	if !strings.Contains(b.UserInfo, "total=0") || !strings.Contains(b.UserInfo, "expire=0") {
		t.Fatalf("unlimited was not expressed the way clients read it: %s", b.UserInfo)
	}
}

func TestBase64IsWhatClientsCanDecode(t *testing.T) {
	db := testDB(t)
	c := seed(t, db, 2)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})

	token, _ := s.EnsureToken(context.Background(), c.ID)
	b, err := s.Serve(context.Background(), token, "base64")
	if err != nil {
		t.Fatal(err)
	}
	// Standard encoding, not URL-safe: that is what the clients decode with.
	decoded, err := base64.StdEncoding.DecodeString(string(b.Body))
	if err != nil {
		t.Fatalf("a client could not decode what we served: %v", err)
	}
	if !strings.Contains(string(decoded), "CONFIG") {
		t.Fatal("the decoded body is not the configuration")
	}
}

// ── the failure cases, which are the ones that get shipped broken ────────────

func TestAnEmptyBundleIsNeverServedAsSuccess(t *testing.T) {
	// A 200 with nothing in it is accepted by the customer's app, shows an
	// empty profile, and gives them nothing to report but "it stopped working".
	db := testDB(t)
	c := seed(t, db, 2)
	s := newSubs(db, &fakeDriver{err: fmt.Errorf("driver is not open")})

	token, _ := s.EnsureToken(context.Background(), c.ID)
	if _, err := s.Serve(context.Background(), token, ""); err == nil {
		t.Fatal("a subscription that rendered nothing was served as a success")
	}
}

func TestAClientWithNoDevicesIsDistinguishedFromABrokenDriver(t *testing.T) {
	// One is the operator's to fix by adding a device; the other is the
	// server's. Reporting them identically sends the operator to the wrong
	// page.
	db := testDB(t)
	c := seed(t, db, 0)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})

	token, _ := s.EnsureToken(context.Background(), c.ID)
	_, err := s.Serve(context.Background(), token, "")
	if err == nil {
		t.Fatal("a client with no devices was served a subscription")
	}
	if !strings.Contains(err.Error(), "no devices") {
		t.Fatalf("the message does not say the client has no devices: %v", err)
	}
}

func TestAnUnknownTokenLooksExactlyLikeARevokedOne(t *testing.T) {
	// Otherwise the endpoint answers "which of these tokens is real".
	db := testDB(t)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})

	_, errUnknown := s.Serve(context.Background(), "neverIssuedAnything", "")
	_, errEmpty := s.Serve(context.Background(), "", "")
	if errUnknown == nil || errEmpty == nil {
		t.Fatal("a token that was never issued was accepted")
	}
	if !strings.Contains(errUnknown.Error(), "not found") ||
		!strings.Contains(errEmpty.Error(), "not found") {
		t.Fatalf("the two answers differ: %v / %v", errUnknown, errEmpty)
	}
}

func TestRotatingBreaksTheOldLinkImmediately(t *testing.T) {
	db := testDB(t)
	c := seed(t, db, 1)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})

	old, _ := s.EnsureToken(context.Background(), c.ID)
	fresh, err := s.RotateToken(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == old {
		t.Fatal("rotating produced the same token")
	}
	if _, err := s.Serve(context.Background(), old, ""); err == nil {
		t.Fatal("the old link still works after rotating")
	}
	if _, err := s.Serve(context.Background(), fresh, ""); err != nil {
		t.Fatalf("the new link does not work: %v", err)
	}
}

func TestTokensAreUnguessableAndNeverRepeat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 400; i++ {
		tok, err := newSubToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) < 30 {
			t.Fatalf("token %q is too short to be unguessable", tok)
		}
		if seen[tok] {
			t.Fatal("a token repeated")
		}
		seen[tok] = true
	}
}

// ── operator input ───────────────────────────────────────────────────────────

func TestThePathCannotShadowThePanel(t *testing.T) {
	db := testDB(t)
	s := newSubs(db, nil)

	for _, bad := range []string{"/api/", "/", "/api/clients/"} {
		_, err := s.SaveSettings(context.Background(), SubSettings{
			Enabled: true, Path: bad, Title: "x", UpdateHours: 12,
		})
		if err == nil {
			t.Errorf("path %q was accepted and would break the panel", bad)
		}
	}
}

func TestThePathIsTidiedRatherThanRefused(t *testing.T) {
	// An operator typing "mylink" means the same thing as "/mylink/", and
	// rejecting the first teaches them nothing.
	db := testDB(t)
	s := newSubs(db, nil)

	got, err := s.SaveSettings(context.Background(), SubSettings{
		Enabled: true, Path: "mylink", Title: "x", UpdateHours: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/mylink/" {
		t.Fatalf("path was stored as %q", got.Path)
	}
}

func TestTheDefaultPathIsNotTheOneEverybodyScansFor(t *testing.T) {
	if DefaultSubPath == "/sub/" {
		t.Fatal("the default path is the one every scanner tries first")
	}
}

func TestAFilenameCannotCarryACustomersOwnHeaders(t *testing.T) {
	// The name is the customer's text and it reaches Content-Disposition.
	for _, in := range []string{
		`a" ; drop`, "line\r\nX-Evil: yes", `../../etc/passwd`, "",
	} {
		got := safeFilename(in)
		if strings.ContainsAny(got, "\r\n\"';/\\ ") {
			t.Errorf("safeFilename(%q) produced %q, which is not safe in a header", in, got)
		}
		if got == "" {
			t.Errorf("safeFilename(%q) produced an empty name", in)
		}
	}
}
