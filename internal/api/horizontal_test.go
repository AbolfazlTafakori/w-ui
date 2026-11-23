package api

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// No route an operator reaches lets one reseller read or change another's
// customer by naming its id: every one, by the table, with rb asking for
// ra's customer (and ra's customer's file, which has the same id here).
// Nothing answers with it, and nothing of it changes.
func TestNoOperatorRouteReachesAnotherResellersCustomer(t *testing.T) {
	mux, sess, db := serversPanel(t)
	var target model.Client
	if err := db.Where("owner_id = (SELECT id FROM admins WHERE username = 'ra')").Order("id").First(&target).Error; err != nil {
		t.Fatal(err)
	}
	var file model.Account
	if err := db.Where("client_id = ?", target.ID).First(&file).Error; err != nil {
		t.Fatal(err)
	}
	if file.ID != target.ID {
		t.Fatalf("the rig changed: ra's customer %d has file %d; the sweep names both by one id", target.ID, file.ID)
	}
	snapshot := func() string {
		var c model.Client
		var accs []model.Account
		db.First(&c, target.ID)
		db.Where("client_id = ?", target.ID).Order("id").Find(&accs)
		return fmt.Sprintf("%+v|%+v", c, accs)
	}
	before := snapshot()
	param := regexp.MustCompile(`\{[a-zA-Z]+\}`)
	tried := 0
	for _, r := range newRouteServer().routes() {
		if r.forOwner() || !(r.Auth || r.Operator) || !strings.Contains(r.Path, "{id}") {
			continue
		}
		path := param.ReplaceAllStringFunc(r.Path, func(p string) string {
			if p == "{id}" {
				return fmt.Sprint(target.ID)
			}
			return "1"
		})
		code, body := send(t, mux, sess["rb"], r.Method, path, "{}")
		tried++
		if code >= 200 && code < 300 {
			t.Errorf("%s %s answered rb with ra's customer: %d %s", r.Method, path, code, body)
		}
		if strings.Contains(body, target.Name) || (target.SubToken != "" && strings.Contains(body, target.SubToken)) {
			t.Errorf("%s %s told rb of ra's customer: %s", r.Method, path, body)
		}
	}
	if tried < 5 {
		t.Fatalf("only %d routes swept; the table changed under the test", tried)
	}
	if after := snapshot(); after != before {
		t.Fatalf("ra's customer changed under rb's requests:\nbefore %s\nafter  %s", before, after)
	}
}
