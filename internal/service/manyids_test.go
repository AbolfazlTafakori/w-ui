package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/operators"
)

// SQLite refuses a statement with 32,767 or more parameters, and each id of
// an IN list is one. Every path here takes a list of customers that grows
// with the panel, and each is run with more customers than that.
func TestPathsOverMoreCustomersThanOneStatementMayName(t *testing.T) {
	if testing.Short() {
		t.Skip("thirty-three thousand customers")
	}
	const n = 33000
	ctx := context.Background()
	p := newSeatPanel(t)
	r := p.operator(t, "big", model.RoleReseller, "", 0, nil)

	rows := make([]model.Client, n)
	for i := range rows {
		rows[i] = model.Client{Name: fmt.Sprintf("c%05d", i), OwnerID: r.ID, Status: model.StatusActive, UsedBytes: 5, QuotaBytes: 10}
	}
	if err := p.db.CreateInBatches(rows, 1000).Error; err != nil {
		t.Fatal(err)
	}
	ids := make([]uint, n)
	files := make([]model.Account, n)
	for i := range rows {
		ids[i] = rows[i].ID
		files[i] = model.Account{ClientID: rows[i].ID, InterfaceID: 1, DeviceName: "d", IP: fmt.Sprintf("10.%d.%d.%d", 100+i/65536, i/256%256, i%256)}
	}
	if err := p.db.CreateInBatches(files, 1000).Error; err != nil {
		t.Fatal(err)
	}
	count := func(q string, args ...any) int64 {
		var c int64
		p.db.Model(&model.ClientGroup{}).Where(q, args...).Count(&c)
		return c
	}

	if got, err := p.clients.AssignGroup(ctx, "vip", ids, false); err != nil || got != n {
		t.Fatalf("add %d to a group: %d, %v", n, got, err)
	}
	if got, err := p.clients.AssignGroup(ctx, "vip", ids, true); err != nil || got != n {
		t.Fatalf("take %d out of a group: %d, %v", n, got, err)
	}
	if _, err := p.clients.AssignGroup(ctx, "", ids, false); err != nil {
		t.Fatalf("clear %d customers' groups: %v", n, err)
	}
	days := 1.0
	if got, err := p.clients.Adjust(ctx, AdjustInput{IDs: ids, AddDays: &days}); err != nil || got == 0 {
		t.Fatalf("adjust %d: %d, %v", n, got, err)
	}
	ifaces := NewInterfaces(p.db, p.clients.pools, quietLog())
	if got, err := ifaces.ResetTunnelUsage(ctx, 1); err != nil || got != n {
		t.Fatalf("reset a tunnel with %d on it: %d, %v", n, got, err)
	}

	// The reseller's label renamed, then the reseller removed keeping them:
	// every customer refiled, then handed to the owner.
	filed := make([]model.ClientGroup, n)
	for i, id := range ids {
		filed[i] = model.ClientGroup{ClientID: id, Name: r.GroupName}
	}
	if err := p.db.CreateInBatches(filed, 1000).Error; err != nil {
		t.Fatal(err)
	}
	label := "renamed-label"
	if _, err := p.admins.Update(ctx, r.ID, operators.AdminInput{GroupName: &label}); err != nil {
		t.Fatalf("rename the label of a reseller with %d customers: %v", n, err)
	}
	if got := count("name = ?", label); got != n {
		t.Fatalf("refiled %d of %d", got, n)
	}
	if err := p.admins.Delete(ctx, r.ID, operators.DeleteKeepClients, p.clients); err != nil {
		t.Fatalf("remove a reseller with %d customers: %v", n, err)
	}
	var left int64
	p.db.Model(&model.Client{}).Where("owner_id = 0").Count(&left)
	if left != n || count("name = ?", label) != 0 {
		t.Fatalf("handed over %d of %d; %d still labelled", left, n, count("name = ?", label))
	}
}
