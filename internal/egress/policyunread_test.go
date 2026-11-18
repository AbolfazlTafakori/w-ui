package egress

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

// countryFiles is country lists that are there, or not.
type countryFiles struct{ have bool }

func (c *countryFiles) Prefixes(cc string) ([]netip.Prefix, error) {
	if !c.have {
		return nil, errors.New("the list for " + cc + " is not downloaded yet")
	}
	return []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}, nil
}

// A block list of addresses and a country still blocks the addresses while
// the country's list is missing -- after a restart, before it is downloaded.
// Before, the one entry that did not read emptied the whole list, and every
// address the operator blocked went through.
func TestABlockListOutlivesAMissingCountryList(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := NewOutbounds(db, nil, quietLog()).EnsureBuiltins(ctx); err != nil {
		t.Fatal(err)
	}
	r := NewRouting(db, nil, quietLog())
	files := &countryFiles{have: true}
	r.SetCountries(files)
	if _, err := r.SaveBasic(ctx, BasicRouting{BlockIPs: []string{"198.51.100.0/24", "geoip:zz"}, BlockPorts: []string{"25", "6881-6889"}}); err != nil {
		t.Fatal(err)
	}
	blocks := func() map[netip.Prefix]bool {
		t.Helper()
		p, err := r.Policy(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[netip.Prefix]bool{}
		for _, a := range p.BlockAddrs {
			out[a] = true
		}
		return out
	}
	if b := blocks(); !b[netip.MustParsePrefix("198.51.100.0/24")] || !b[netip.MustParsePrefix("203.0.113.0/24")] {
		t.Fatalf("with the country's list: %v", b)
	}
	files.have = false
	b := blocks()
	if !b[netip.MustParsePrefix("198.51.100.0/24")] {
		t.Fatalf("the country's list missing, the blocked addresses are %v: the rest of the list went with it", b)
	}
	if b[netip.MustParsePrefix("203.0.113.0/24")] {
		t.Fatal("a country whose list is missing still blocks its old addresses")
	}
}
