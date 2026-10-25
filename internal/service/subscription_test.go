package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
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
	"github.com/abolfazl/w-ui/internal/endpoints"
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
	pool := backend.NewPool(quietLog())
	if drv != nil {
		// Registered directly: the fake needs no kernel, and going through
		// Open would ask the registry for a real driver.
		pool.Put(&model.Interface{ID: 1, Name: "wg0", Protocol: model.ProtocolWireGuard}, drv)
	}
	return NewSubscriptions(db, pool, endpoints.NewHosts(db, quietLog()), quietLog())
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
	// Three files, one a file: written one after another they were one file
	// no app imports. Each named apart, or the app has the second "already".
	if b.ContentType != "application/zip" || !strings.HasSuffix(b.Filename, ".zip") {
		t.Fatalf("three devices served as %s %s, want a zip", b.ContentType, b.Filename)
	}
	zr, err := zip.NewReader(bytes.NewReader(b.Body), int64(len(b.Body)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		rc.Close()
		if strings.Count(string(body), "CONFIG") != 1 {
			t.Fatalf("%s holds %q", f.Name, body)
		}
		if f.Modified.Year() < 2020 {
			t.Fatalf("%s is dated %s", f.Name, f.Modified)
		}
		names[f.Name] = true
	}
	if len(names) != 3 {
		t.Fatalf("files %v, want three named apart", names)
	}
}

// One device is its file, as it always was.
func TestOneDeviceIsItsOwnFile(t *testing.T) {
	db := testDB(t)
	c := seed(t, db, 1)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	token, _ := s.EnsureToken(context.Background(), c.ID)
	b, err := s.Serve(context.Background(), token, "")
	if err != nil {
		t.Fatal(err)
	}
	if b.ContentType != "text/plain; charset=utf-8" || strings.Count(string(b.Body), "CONFIG") != 1 {
		t.Fatalf("one device served as %s: %q", b.ContentType, b.Body)
	}
}

// A format the subscription is not served in is said, not answered with
// something else.
func TestAnUnknownFormatIsRefused(t *testing.T) {
	db := testDB(t)
	c := seed(t, db, 1)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	token, _ := s.EnsureToken(context.Background(), c.ID)
	for _, f := range []string{"clash", "singbox", "json"} {
		if _, err := s.Serve(context.Background(), token, f); !errors.Is(err, ErrUnknownFormat) {
			t.Fatalf("format %s: %v", f, err)
		}
	}
	for _, f := range []string{"", "conf", "CONF", "base64", "zip"} {
		if _, err := s.Serve(context.Background(), token, f); err != nil {
			t.Fatalf("format %q: %v", f, err)
		}
	}
}

// base64 of several files that are no links is refused rather than served
// as one text no app reads.
func TestBase64OfSeveralFilesIsLinksOrNothing(t *testing.T) {
	db := testDB(t)
	c := seed(t, db, 2)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	token, _ := s.EnsureToken(context.Background(), c.ID)
	if _, err := s.Serve(context.Background(), token, "base64"); !errors.Is(err, ErrSeveralFiles) {
		t.Fatalf("base64 of two files that are no links: %v", err)
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

// A customer counted before there were two counters has less in upload and
// download than they were charged; their app adds the two up, and must show
// what the panel charged.
func TestTheAppIsToldWhatWasCharged(t *testing.T) {
	db := testDB(t)
	c := seed(t, db, 1)
	if err := db.Model(c).UpdateColumns(map[string]any{"used_bytes": 5000, "up_bytes": 300, "down_bytes": 1200}).Error; err != nil {
		t.Fatal(err)
	}
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	token, _ := s.EnsureToken(context.Background(), c.ID)
	b, err := s.Serve(context.Background(), token, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.UserInfo, "upload=300; download=4700;") {
		t.Fatalf("the app is told %s, charged 5000", b.UserInfo)
	}
	page, err := s.StatusFor(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if page.UpBytes+page.DownBytes != page.UsedBytes {
		t.Fatalf("the page shows %d up and %d down of %d", page.UpBytes, page.DownBytes, page.UsedBytes)
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
	c := seed(t, db, 1)
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
		got := endpoints.SafeFilename(in)
		if strings.ContainsAny(got, "\r\n\"';/\\ ") {
			t.Errorf("endpoints.SafeFilename(%q) produced %q, which is not safe in a header", in, got)
		}
		if got == "" {
			t.Errorf("endpoints.SafeFilename(%q) produced an empty name", in)
		}
	}
}

// bundleText is every file in a bundle as one text, for a test looking for
// what is in them: a zip's entries, or the body as it is.
func bundleText(t *testing.T, b *Bundle) string {
	t.Helper()
	if b.ContentType != "application/zip" {
		return string(b.Body)
	}
	zr, err := zip.NewReader(bytes.NewReader(b.Body), int64(len(b.Body)))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(rc)
		rc.Close()
		out.Write(body)
		out.WriteByte('\n')
	}
	return out.String()
}
