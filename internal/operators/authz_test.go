package operators

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/fault"
)

func authzDB(t *testing.T) *Admins {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Node{Name: "local", Kind: model.KindLocal}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		iface := &model.Interface{
			Name: fmt.Sprintf("wg%d", i), Protocol: model.ProtocolWireGuard,
			ListenPort: 51820 + i, Subnet: fmt.Sprintf("10.8.%d.0/24", i),
			EndpointHost: "vpn.example.com", NodeID: 1,
		}
		if err := db.Create(iface).Error; err != nil {
			t.Fatal(err)
		}
	}
	return NewAdmins(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func mkAdmin(t *testing.T, s *Admins, name string, quota uint64, ifaces []uint) *model.Admin {
	t.Helper()
	on := true
	a, err := s.Create(context.Background(), AdminInput{
		Username: name, Password: "correct-horse", Role: model.RoleAdmin,
		Enabled: &on, QuotaBytes: &quota, InterfaceIDs: ifaces,
	})
	if err != nil {
		t.Fatalf("owner create admin %s: %v", name, err)
	}
	return a
}

const gb = uint64(1) << 30

// An admin creates resellers only, under themselves, within their own
// ceiling, and never reaches anyone else's operators.
func TestAnAdminManagesOnlyTheirOwnResellers(t *testing.T) {
	s := authzDB(t)
	ali := mkAdmin(t, s, "ali", 100*gb, []uint{1, 2})
	beh := mkAdmin(t, s, "beh", 100*gb, []uint{1, 3})

	asAli := WithCaller(context.Background(), ali)
	q := 30 * gb

	// A reseller under ali: forced to the reseller role and to ali as parent.
	r1, err := s.Create(asAli, AdminInput{Username: "res1", Password: "correct-horse",
		Role: model.RoleReseller, QuotaBytes: &q, InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatalf("ali create reseller: %v", err)
	}
	if r1.Role != model.RoleReseller || r1.ParentAdminID != ali.ID {
		t.Fatalf("ali's reseller is role %q parent %d, want reseller under %d", r1.Role, r1.ParentAdminID, ali.ID)
	}

	// An admin cannot mint an admin.
	if _, err := s.Create(asAli, AdminInput{Username: "xadm", Password: "correct-horse", Role: model.RoleAdmin}); !errors.Is(err, fault.ErrInvalid) {
		t.Fatalf("ali created an admin: %v", err)
	}
	// Nor hand down a tunnel that is not theirs...
	if _, err := s.Create(asAli, AdminInput{Username: "ytun", Password: "correct-horse",
		Role: model.RoleReseller, InterfaceIDs: []uint{3}}); !errors.Is(err, fault.ErrInvalid) {
		t.Fatalf("ali handed down an unowned tunnel: %v", err)
	}
	// ...nor more traffic than they have left.
	tooMuch := 200 * gb
	if _, err := s.Create(asAli, AdminInput{Username: "zquota", Password: "correct-horse",
		Role: model.RoleReseller, QuotaBytes: &tooMuch, InterfaceIDs: []uint{1}}); !errors.Is(err, fault.ErrInvalid) {
		t.Fatalf("ali handed down more than their allowance: %v", err)
	}

	// beh's reseller, to prove isolation.
	bq := 10 * gb
	b1, err := s.Create(WithCaller(context.Background(), beh), AdminInput{Username: "beh1",
		Password: "correct-horse", Role: model.RoleReseller, QuotaBytes: &bq, InterfaceIDs: []uint{1}})
	if err != nil {
		t.Fatal(err)
	}

	// List, as ali, is only ali's own reseller -- no admin, no owner, no beh.
	list, err := s.List(asAli)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != r1.ID {
		t.Fatalf("ali's operator list is %+v, want only r1", list)
	}

	// ali cannot reach beh's reseller, nor beh, nor themselves, by id.
	for _, id := range []uint{b1.ID, beh.ID, ali.ID} {
		if _, err := s.Get(asAli, id); !errors.Is(err, fault.ErrNotFound) {
			t.Errorf("ali read operator %d: %v", id, err)
		}
		if _, err := s.Update(asAli, id, AdminInput{Note: "nope"}); !errors.Is(err, fault.ErrNotFound) {
			t.Errorf("ali changed operator %d: %v", id, err)
		}
		if err := s.Delete(asAli, id, DeleteKeepClients, nil); !errors.Is(err, fault.ErrNotFound) {
			t.Errorf("ali deleted operator %d: %v", id, err)
		}
	}

	// ali may change their own reseller, but not past their allowance.
	if _, err := s.Update(asAli, r1.ID, AdminInput{Note: "fine"}); err != nil {
		t.Errorf("ali could not change their own reseller: %v", err)
	}
	if _, err := s.Update(asAli, r1.ID, AdminInput{QuotaBytes: &tooMuch}); !errors.Is(err, fault.ErrInvalid) {
		t.Errorf("ali raised their reseller past their own allowance: %v", err)
	}
}

// The owner grants an admin pages with a checkbox each: the default is every
// page, a list is taken as given, and the backend can say which an admin has.
func TestAnAdminsMenuGrants(t *testing.T) {
	s := authzDB(t)
	on := true

	// No list on create: the admin gets every grantable page.
	full, err := s.Create(context.Background(), AdminInput{Username: "full",
		Password: "correct-horse", Role: model.RoleAdmin, Enabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.MenuIDs(context.Background(), full.ID); len(got) != len(model.GrantableMenus()) {
		t.Fatalf("a default admin has pages %v, want all", got)
	}

	// A list is taken as given.
	some, err := s.Create(context.Background(), AdminInput{Username: "some",
		Password: "correct-horse", Role: model.RoleAdmin, Enabled: &on,
		MenuIDs: []string{model.MenuGroups}})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.HasMenu(context.Background(), some.ID, model.MenuGroups); !ok {
		t.Error("the admin was not granted the groups page they were given")
	}
	if ok, _ := s.HasMenu(context.Background(), some.ID, model.MenuClients); ok {
		t.Error("the admin has the clients page they were never given")
	}

	// An unknown page is refused, not quietly dropped.
	if _, err := s.Create(context.Background(), AdminInput{Username: "bad",
		Password: "correct-horse", Role: model.RoleAdmin, Enabled: &on,
		MenuIDs: []string{"routing"}}); !errors.Is(err, fault.ErrInvalid) {
		t.Errorf("a bad page was accepted: %v", err)
	}

	// An update with an empty list is the owner unchecking every page.
	if _, err := s.Update(context.Background(), some.ID, AdminInput{MenuIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.MenuIDs(context.Background(), some.ID); len(got) != 0 {
		t.Errorf("after unchecking all, the admin still has %v", got)
	}
}
