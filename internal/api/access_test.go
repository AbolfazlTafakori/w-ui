package api

import (
	"net/http"
	"sort"
	"strings"
	"testing"
)

// What a reseller can reach, pinned.
//
// The gate is derived from the heading a route is documented under, which
// makes the default the strictest one: a new endpoint is the owner's unless
// somebody says otherwise. This test is the other half of that -- it lists,
// exactly, everything a signed-in operator who is not the owner may call, so
// adding to that set is a deliberate edit here rather than a side effect of
// choosing a heading.
func TestWhatANonOwnerMayReach(t *testing.T) {
	want := []string{
		// Their own account, and signing in and out of it.
		"GET /api/auth/me",
		"PATCH /api/auth/me",
		"POST /api/auth/password",
		"POST /api/auth/totp/confirm",
		"POST /api/auth/totp/disable",
		"POST /api/auth/totp/start",

		// The two pages that are theirs.
		"GET /api/clients",
		"POST /api/clients",
		"GET /api/clients/{id}",
		"PATCH /api/clients/{id}",
		"DELETE /api/clients/{id}",
		"GET /api/clients/{id}/configs",
		"POST /api/clients/{id}/devices",
		"POST /api/clients/{id}/reset",
		"POST /api/clients/{id}/rotate-keys",
		"POST /api/clients/adjust",
		"POST /api/clients/batch",
		"POST /api/clients/bulk",
		"GET /api/clients/export",
		"POST /api/clients/import",
		"POST /api/clients/purge",
		"POST /api/clients/reset-all",
		"POST /api/clients/servers/attach",
		"POST /api/clients/servers/detach",
		"GET /api/groups",
		"POST /api/groups",
		"POST /api/groups/action",
		"POST /api/groups/assign",
		"POST /api/groups/delete",
		"GET /api/groups/names",
		"POST /api/groups/rename",
		"GET /api/devices/{id}/profile",
		"GET /api/devices/{id}/profiles",
		"DELETE /api/devices/{id}",

		// The few under an owner's heading they cannot work without.
		"GET /api/interfaces",
		"GET /api/settings",
		"GET /api/clients/{id}/subscription",
		"POST /api/clients/{id}/subscription/rotate",
	}
	sort.Strings(want)

	var got []string
	for _, r := range newRouteServer().routes() {
		// Unauthenticated routes are the sign-in page's and are not part of
		// this question.
		if !r.Auth && !r.Operator {
			continue
		}
		if r.forOwner() {
			continue
		}
		got = append(got, r.Method+" "+r.Path)
	}
	sort.Strings(got)

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("what a non-owner may reach has changed.\n got:\n  %s\nwant:\n  %s",
			strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// Nothing that changes the machine is reachable by anyone but the owner.
func TestTheMachineIsTheOwnersAlone(t *testing.T) {
	ownersGroups := map[string]bool{
		"Interfaces": true, "Nodes": true, "Outbounds": true, "Routing": true,
		"Hosts": true, "Engine": true, "Server": true, "Settings": true,
		"Operators": true,
	}
	for _, r := range newRouteServer().routes() {
		if !ownersGroups[r.Group] || r.forOwner() {
			continue
		}
		// The handful of reads a reseller needs are allowed; a write never is.
		if r.Method != http.MethodGet {
			t.Errorf("%s %s changes the machine and is not the owner's alone", r.Method, r.Path)
		}
	}
}
