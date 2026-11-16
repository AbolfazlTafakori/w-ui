package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// seedUsers is a plan for n users on one tunnel, files named as the panel
// names them: device-1 for the first, user-2 on.
func seedUsers(t *testing.T, db *gorm.DB, n int) *model.Client {
	t.Helper()
	c := seed(t, db, 0)
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("user-%d", i)
		if i == 1 {
			name = "device-1"
		}
		acc := model.Account{
			ClientID: c.ID, InterfaceID: 1, DeviceName: name,
			IP: fmt.Sprintf("10.66.1.%d", i+1), UpBytes: uint64(i) << 20,
		}
		if err := db.Create(&acc).Error; err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// devicesIn is the account ids a served bundle holds, by what the fake
// driver writes into each file.
func devicesIn(t *testing.T, b *Bundle) []string {
	t.Helper()
	var bodies []string
	if b.ContentType == "application/zip" {
		zr, err := zip.NewReader(bytes.NewReader(b.Body), int64(len(b.Body)))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range zr.File {
			rc, _ := f.Open()
			body, _ := io.ReadAll(rc)
			rc.Close()
			bodies = append(bodies, strings.TrimSpace(string(body)))
		}
	} else {
		bodies = append(bodies, strings.TrimSpace(string(b.Body)))
	}
	return bodies
}

func TestTheCustomersOwnLinkIsUntouchedByUsersLinks(t *testing.T) {
	db := testDB(t)
	c := seedUsers(t, db, 3)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	ctx := context.Background()

	token, err := s.EnsureToken(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Serve(ctx, token, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserLinks(ctx, c.ID, "panel.example.com"); err != nil {
		t.Fatal(err)
	}
	after, err := s.Serve(ctx, token, "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Body, after.Body) || before.UserInfo != after.UserInfo || before.Filename != after.Filename {
		t.Fatal("issuing users' links changed what the customer's own link serves")
	}
	if got := len(devicesIn(t, after)); got != 3 {
		t.Fatalf("the customer's link carries %d files, want all 3", got)
	}
	var reread model.Client
	db.First(&reread, c.ID)
	if reread.SubToken != token {
		t.Fatal("the customer's token changed")
	}
}

func TestEachUsersLinkCarriesOnlyTheirOwnFiles(t *testing.T) {
	db := testDB(t)
	c := seedUsers(t, db, 3)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	ctx := context.Background()

	links, err := s.UserLinks(ctx, c.ID, "panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 3 {
		t.Fatalf("%d links, want one per user", len(links))
	}
	var accs []model.Account
	db.Where("client_id = ?", c.ID).Order("id").Find(&accs)
	seen := map[string]bool{}
	for i, u := range links {
		if u.User != i+1 || u.Token == "" || seen[u.Token] {
			t.Fatalf("user %d: %+v", i+1, u)
		}
		seen[u.Token] = true
		if !strings.HasSuffix(u.Link, "/"+u.Token) {
			t.Fatalf("link %q does not end in its token", u.Link)
		}
		if u.UpBytes != uint64(i+1)<<20 {
			t.Fatalf("user %d counted %d bytes up", u.User, u.UpBytes)
		}
		b, err := s.Serve(ctx, u.Token, "")
		if err != nil {
			t.Fatal(err)
		}
		got := devicesIn(t, b)
		want := fmt.Sprintf("CONFIG device=%d", accs[i].ID)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("user %d's link served %q, want only %q", u.User, got, want)
		}
		// The plan's figures, which are the user's too: the allowance is
		// shared, and their app shows what is left of it.
		if b.UserInfo != userInfo(c) {
			t.Fatalf("user %d told %q", u.User, b.UserInfo)
		}
	}

	// Asked again, the same links: nothing is reissued by looking.
	again, err := s.UserLinks(ctx, c.ID, "panel.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for i := range again {
		if again[i].Token != links[i].Token {
			t.Fatal("listing the links again changed them")
		}
	}
}

func TestAUsersPageShowsThemAloneAndNumberedAsThePlanNumbersThem(t *testing.T) {
	db := testDB(t)
	c := seedUsers(t, db, 3)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	ctx := context.Background()

	links, _ := s.UserLinks(ctx, c.ID, "h")
	page, err := s.PageFor(ctx, links[1].Token, links[1].Link)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Devices) != 1 || page.Devices[0].User != 2 {
		t.Fatalf("user 2's page shows %+v", page.Devices)
	}
	if page.SubURL != links[1].Link {
		t.Fatal("a user's page offers some other link")
	}
	// Another user's file is not theirs to download.
	var other model.Account
	db.Where("client_id = ? AND device_name = ?", c.ID, "user-3").First(&other)
	if _, err := s.DeviceConfig(ctx, links[1].Token, other.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("user 2 downloaded user 3's file: %v", err)
	}
	if _, err := s.DeviceConfig(ctx, links[1].Token, page.Devices[0].ID, 0); err != nil {
		t.Fatalf("user 2 could not download their own file: %v", err)
	}

	// The customer's page numbers the same users the same way.
	token, _ := s.EnsureToken(ctx, c.ID)
	whole, err := s.PageFor(ctx, token, "")
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range whole.Devices {
		if d.User != i+1 {
			t.Fatalf("the customer's page numbers file %d as user %d", i, d.User)
		}
	}
}

func TestRotatingOneUsersLinkLeavesTheRestWorking(t *testing.T) {
	db := testDB(t)
	c := seedUsers(t, db, 3)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	ctx := context.Background()
	token, _ := s.EnsureToken(ctx, c.ID)

	links, _ := s.UserLinks(ctx, c.ID, "h")
	fresh, err := s.RotateUserLink(ctx, c.ID, 2, "h")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Token == links[1].Token || fresh.User != 2 {
		t.Fatalf("rotated to %+v", fresh)
	}
	if _, err := s.Serve(ctx, links[1].Token, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the old link still answers: %v", err)
	}
	if ok, _ := s.TokenExists(ctx, links[1].Token); ok {
		t.Fatal("the old link is still a link")
	}
	for _, tok := range []string{fresh.Token, links[0].Token, links[2].Token, token} {
		if _, err := s.Serve(ctx, tok, ""); err != nil {
			t.Fatalf("a link that should work does not: %v", err)
		}
		if ok, _ := s.TokenExists(ctx, tok); !ok {
			t.Fatal("a live link is not counted as one")
		}
	}
	if _, err := s.RotateUserLink(ctx, c.ID, 9, "h"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("rotating a user the plan does not have: %v", err)
	}
}

func TestAUserRemovedTakesTheirLinkAndNobodyInheritsIt(t *testing.T) {
	db := testDB(t)
	c := seedUsers(t, db, 3)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	ctx := context.Background()

	links, _ := s.UserLinks(ctx, c.ID, "h")
	var two model.Account
	db.Where("client_id = ? AND device_name = ?", c.ID, "user-2").First(&two)
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.Account{}, two.ID).Error; err != nil {
			return err
		}
		return dropUserLink(tx, c.ID, two.DeviceName)
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Serve(ctx, links[1].Token, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a removed user's link still answers: %v", err)
	}
	var n int64
	db.Model(&model.UserSub{}).Where("client_id = ?", c.ID).Count(&n)
	if n != 2 {
		t.Fatalf("%d users' links left, want 2", n)
	}
	// User 3 is now the plan's second user, and keeps their own link and
	// their own file -- not user 2's.
	b, err := s.Serve(ctx, links[2].Token, "")
	if err != nil {
		t.Fatal(err)
	}
	var three model.Account
	db.Where("client_id = ? AND device_name = ?", c.ID, "user-3").First(&three)
	if got := devicesIn(t, b); len(got) != 1 || got[0] != fmt.Sprintf("CONFIG device=%d", three.ID) {
		t.Fatalf("user 3's link served %q", got)
	}
	// A user given the old name again gets a link of their own.
	if err := db.Create(&model.Account{ClientID: c.ID, InterfaceID: 1, DeviceName: "user-2", IP: "10.66.1.99"}).Error; err != nil {
		t.Fatal(err)
	}
	again, _ := s.UserLinks(ctx, c.ID, "h")
	for _, u := range again {
		if u.Token == links[1].Token {
			t.Fatal("a new user was handed a removed user's link")
		}
	}
}

func TestADeletedCustomerTakesTheUsersLinksWithThem(t *testing.T) {
	db := testDB(t)
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatal(err)
	}
	c := seedUsers(t, db, 2)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	ctx := context.Background()
	links, _ := s.UserLinks(ctx, c.ID, "h")
	if err := db.Delete(&model.Client{}, c.ID).Error; err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&model.UserSub{}).Count(&n)
	if n != 0 {
		t.Fatalf("%d users' links outlived their customer", n)
	}
	if ok, _ := s.TokenExists(ctx, links[0].Token); ok {
		t.Fatal("a deleted customer's user link is still a link")
	}
}

func TestACustomersIDCannotBeAUsersLink(t *testing.T) {
	db := testDB(t)
	c := seedUsers(t, db, 2)
	other := model.Client{Name: "Sina", Protocol: model.ProtocolWireGuard, Status: model.StatusActive}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	clients := &Clients{db: db, log: quietLog()}
	ctx := context.Background()
	links, _ := s.UserLinks(ctx, c.ID, "h")
	if _, err := clients.checkSubID(ctx, other.ID, links[0].Token); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a customer's id was set to a user's link: %v", err)
	}
}

func TestThePlansPageListsEachUsersLinkAndAUsersPageDoesNot(t *testing.T) {
	db := testDB(t)
	c := seedUsers(t, db, 3)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	ctx := context.Background()
	token, _ := s.EnsureToken(ctx, c.ID)

	page, err := s.PageFor(ctx, token, "https://panel.example.com/subscribe/"+token)
	if err != nil {
		t.Fatal(err)
	}
	links, _ := s.UserLinks(ctx, c.ID, "panel.example.com")
	if len(page.Users) != 3 {
		t.Fatalf("the plan's page lists %d users, want 3", len(page.Users))
	}
	for i, u := range page.Users {
		if u.User != i+1 || u.Token != links[i].Token || u.Link != "https://panel.example.com/subscribe/"+u.Token {
			t.Fatalf("user %d listed as %+v", i+1, u)
		}
	}
	mine, err := s.PageFor(ctx, links[0].Token, "https://panel.example.com/subscribe/"+links[0].Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(mine.Users) != 0 {
		t.Fatal("a user's own page lists the other users' links")
	}

	// A plan for one has no users to list.
	one := model.Client{Name: "Sina", Protocol: model.ProtocolWireGuard, Status: model.StatusActive}
	if err := db.Create(&one).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Account{ClientID: one.ID, InterfaceID: 1, DeviceName: "device-1", IP: "10.66.2.2"}).Error; err != nil {
		t.Fatal(err)
	}
	tok, _ := s.EnsureToken(ctx, one.ID)
	single, err := s.PageFor(ctx, tok, "https://h/subscribe/"+tok)
	if err != nil {
		t.Fatal(err)
	}
	if len(single.Users) != 0 {
		t.Fatal("a plan for one lists users")
	}
}

// fileDB is a database on disk, so several connections see one database the
// way the panel's do; an in-memory one is a database per connection.
func fileDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "wui.db") + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = raw.Close() })
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestManyFirstOpeningsAtOnceIssueOneLinkPerUser(t *testing.T) {
	db := fileDB(t)
	c := seedUsers(t, db, 20)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	ctx := context.Background()

	const callers = 32
	got := make([][]UserLink, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = s.UserLinks(ctx, c.ID, "h")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	for i := 1; i < callers; i++ {
		for u := range got[0] {
			if got[i][u].Token != got[0][u].Token {
				t.Fatalf("callers were shown different links for user %d", u+1)
			}
		}
	}
	var n int64
	db.Model(&model.UserSub{}).Where("client_id = ?", c.ID).Count(&n)
	if n != 20 {
		t.Fatalf("%d links for 20 users", n)
	}
}

func TestReadingLinksAgainWritesNothing(t *testing.T) {
	db := testDB(t)
	c := seedUsers(t, db, 50)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	ctx := context.Background()
	if _, err := s.UserLinks(ctx, c.ID, "h"); err != nil {
		t.Fatal(err)
	}
	writes, reads := 0, 0
	db.Callback().Create().Before("gorm:create").Register("count:create", func(*gorm.DB) { writes++ })
	db.Callback().Update().Before("gorm:update").Register("count:update", func(*gorm.DB) { writes++ })
	db.Callback().Delete().Before("gorm:delete").Register("count:delete", func(*gorm.DB) { writes++ })
	db.Callback().Query().Before("gorm:query").Register("count:query", func(*gorm.DB) { reads++ })
	if _, err := s.UserLinks(ctx, c.ID, "h"); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatalf("listing issued links again wrote %d times", writes)
	}
	// A fixed handful, however many users: not a query per user.
	if reads > 8 {
		t.Fatalf("listing 50 users' links read the database %d times", reads)
	}
}

func TestALinkLeftBehindDoesNotPassToTheNextUserOfTheName(t *testing.T) {
	db := testDB(t)
	c := seedUsers(t, db, 3)
	s := newSubs(db, &fakeDriver{body: "CONFIG"})
	ctx := context.Background()
	links, _ := s.UserLinks(ctx, c.ID, "h")

	// User 3's file removed by a path that does not drop their link, and
	// a new user given the name a moment later.
	if err := db.Where("client_id = ? AND device_name = ?", c.ID, "user-3").Delete(&model.Account{}).Error; err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := db.Create(&model.Account{ClientID: c.ID, InterfaceID: 1, DeviceName: "user-3", IP: "10.66.1.77"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Serve(ctx, links[2].Token, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the old user 3's link reached the new user 3's file: %v", err)
	}
	again, err := s.UserLinks(ctx, c.ID, "h")
	if err != nil {
		t.Fatal(err)
	}
	if again[2].Token == links[2].Token {
		t.Fatal("the new user 3 was listed with the old user's link")
	}
	if _, err := s.Serve(ctx, again[2].Token, ""); err != nil {
		t.Fatalf("the new user 3's own link does not work: %v", err)
	}
	if ok, _ := s.TokenExists(ctx, links[2].Token); ok {
		t.Fatal("the stale link is still counted as a link")
	}
	// Users 1 and 2 kept theirs throughout.
	for i := 0; i < 2; i++ {
		if again[i].Token != links[i].Token {
			t.Fatalf("user %d's link changed", i+1)
		}
	}
}
