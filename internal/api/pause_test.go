package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
	"github.com/abolfazl/w-ui/internal/scope"
	"github.com/abolfazl/w-ui/internal/service"
)

// pauseServer is the part of the server an operator's requests pass through
// on their way to the customers: the scope they are served under, over a real
// schema.
func pauseServer(t *testing.T) (*Server, *gorm.DB, *service.Clients) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatal(err)
	}
	if err := scope.Register(db); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := db.Create(&model.Node{Name: "local", Kind: model.KindLocal}).Error; err != nil {
		t.Fatal(err)
	}
	iface := &model.Interface{Name: "wg0", Protocol: model.ProtocolWireGuard, ListenPort: 51820,
		Subnet: "10.9.0.0/24", EndpointHost: "vpn.example.com", NodeID: 1}
	if err := db.Create(iface).Error; err != nil {
		t.Fatal(err)
	}
	pools := ipam.NewPools()
	if _, err := pools.Add(iface.ID, iface.Subnet); err != nil {
		t.Fatal(err)
	}
	return &Server{db: db, log: log, admins: service.NewAdmins(db, log)}, db, service.NewClients(db, pools, log)
}

func reseller(t *testing.T, db *gorm.DB, a model.Admin) *model.Admin {
	t.Helper()
	a.Role, a.PasswordHash = model.RoleReseller, "x"
	if a.Username == "" {
		a.Username = "reza"
	}
	// Enabled defaults to true in the schema, and the insert reads the
	// default back into the struct -- so what was asked for is kept aside,
	// and a false is written after the row exists.
	enabled := a.Enabled
	if err := db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	if !enabled {
		if err := db.Model(&a).Update("enabled", false).Error; err != nil {
			t.Fatal(err)
		}
		a.Enabled = false
	}
	if err := db.Create(&model.AdminInterface{AdminID: a.ID, InterfaceID: 1}).Error; err != nil {
		t.Fatal(err)
	}
	return &a
}

// Who a correct password still turns away. Only the owner's decision keeps an
// operator out; a lapse lets them in, read-only, to see why.
func TestWhoIsTurnedAwayAtSignIn(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	cases := []struct {
		name  string
		admin model.Admin
		out   bool
	}{
		{"a reseller in good standing", model.Admin{Role: model.RoleReseller, Enabled: true}, false},
		{"a reseller switched off", model.Admin{Role: model.RoleReseller}, true},
		{"a reseller whose term has ended", model.Admin{Role: model.RoleReseller, Enabled: true, ExpiresAt: &past}, false},
		{"a reseller whose traffic is used up", model.Admin{Role: model.RoleReseller, Enabled: true, QuotaBytes: 1, UsedBytes: 1}, false},
		{"a reseller switched off whose term has ended", model.Admin{Role: model.RoleReseller, ExpiresAt: &past}, true},
		{"an administrator switched off", model.Admin{Role: model.RoleAdmin}, true},
		{"the owner, whatever the switch says", model.Admin{Role: model.RoleOwner}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := signInRefusal(&tc.admin, now)
			if (got != "") != tc.out {
				t.Fatalf("refusal %q, want refused=%v", got, tc.out)
			}
			if tc.out && got != model.PauseMessage(model.PauseSwitchedOff) {
				t.Fatalf("refused with %q, want the switched-off message", got)
			}
		})
	}
}

// A paused reseller is told why on every change they try, in the words for
// their reason -- and can still read what they hold.
func TestAPausedResellerIsToldWhyOnEveryChange(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	cases := []struct {
		name   string
		admin  model.Admin
		reason string
	}{
		{"switched off", model.Admin{Username: "a"}, model.PauseSwitchedOff},
		{"term ended", model.Admin{Username: "b", Enabled: true, ExpiresAt: &past}, model.PauseTermEnded},
		{"traffic used", model.Admin{Username: "c", Enabled: true, QuotaBytes: 10, UsedBytes: 10}, model.PauseTrafficUsed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db, clients := pauseServer(t)
			// A customer sold while they were in good standing.
			good := reseller(t, db, model.Admin{Username: tc.admin.Username + "-good", Enabled: true})
			a := reseller(t, db, tc.admin)
			sold, err := clients.Create(scoped(t, srv, good), service.CreateInput{Name: "theirs", InterfaceIDs: []uint{1}})
			if err != nil {
				t.Fatal(err)
			}
			db.Model(&model.Client{}).Where("id = ?", sold.ID).Update("owner_id", a.ID)

			ctx := scoped(t, srv, a)
			want := model.PauseMessage(tc.reason)

			name := "renamed"
			days := 30.0
			attempts := map[string]error{}
			_, attempts["sell a customer"] = clients.Create(ctx, service.CreateInput{Name: "new", InterfaceIDs: []uint{1}})
			_, attempts["change a customer"] = clients.Update(ctx, sold.ID, service.UpdateInput{Name: &name})
			_, attempts["reset traffic"] = clients.ResetTraffic(ctx, sold.ID)
			_, attempts["extend in bulk"] = clients.Adjust(ctx, service.AdjustInput{IDs: []uint{sold.ID}, AddDays: &days})
			_, attempts["add a device"] = clients.AddDevice(ctx, sold.ID, "extra")
			attempts["delete a customer"] = clients.Delete(ctx, sold.ID)
			for what, err := range attempts {
				if err == nil {
					t.Errorf("a reseller paused (%s) could %s", tc.name, what)
					continue
				}
				if !errors.Is(err, service.ErrInvalid) || !strings.Contains(err.Error(), want) {
					t.Errorf("%s was refused with %v, want %q", what, err, want)
				}
			}

			if _, err := clients.Get(ctx, sold.ID); err != nil {
				t.Errorf("a paused reseller could not read their own customer: %v", err)
			}
		})
	}
}

// The owner and a panel administrator are never paused, and are never barred
// from changing anything.
func TestTheOwnerAndAnAdministratorAreNeverBarred(t *testing.T) {
	srv, _, _ := pauseServer(t)
	for _, a := range []model.Admin{
		{ID: 1, Role: model.RoleOwner},
		{ID: 2, Role: model.RoleAdmin, Enabled: true},
	} {
		sc, err := srv.scopeFor(context.Background(), &a)
		if err != nil {
			t.Fatal(err)
		}
		if sc.Barred != "" || sc.Restricted {
			t.Errorf("%s is served under %+v", a.Role, sc)
		}
	}
}

func scoped(t *testing.T, srv *Server, a *model.Admin) context.Context {
	t.Helper()
	sc, err := srv.scopeFor(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	return service.WithScope(context.Background(), sc)
}
