package service

import (
	"context"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/ipam"
)

// A group made on the groups page before anyone is in it is offered by
// the picker alongside the labels customers already carry.
func TestGroupNamesIncludeEmptyGroups(t *testing.T) {
	db := testDB(t)
	if err := db.Create(&model.Client{Name: "a", Status: model.StatusActive, Group: "labelled"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Group{Name: "made-empty"}).Error; err != nil {
		t.Fatal(err)
	}
	names, err := NewClients(db, ipam.NewPools(), quietLog()).ListGroupNames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "labelled" || names[1] != "made-empty" {
		t.Fatalf("names: %v", names)
	}
}
