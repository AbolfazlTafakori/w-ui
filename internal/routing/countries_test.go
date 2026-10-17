package routing

import (
	"errors"
	"net/netip"
	"testing"
)

type fakeCountries map[string][]netip.Prefix

func (f fakeCountries) Prefixes(cc string) ([]netip.Prefix, error) {
	if ps, ok := f[cc]; ok {
		return ps, nil
	}
	return nil, errors.New("no such country")
}

// A country entry is the ranges the panel's country lists give it; a panel
// without them refuses one, rather than taking it as nothing.
func TestACountryIsTheRangesItsListGives(t *testing.T) {
	ir := []netip.Prefix{netip.MustParsePrefix("5.200.0.0/16")}
	got, err := ParseTargets([]string{"geoip:IR", "10.0.0.0/8"}, fakeCountries{"ir": ir})
	if err != nil || len(got) != 2 {
		t.Fatalf("%v, %v", got, err)
	}
	if _, err := ParseTargets([]string{"geoip:ir"}, nil); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("without country lists: %v", err)
	}
	if _, err := ParseTargets([]string{"geoip:zz"}, fakeCountries{"ir": ir}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("an unknown country: %v", err)
	}
	if got, err := ParseTargets([]string{"geoip:private"}, nil); err != nil || len(got) == 0 {
		t.Fatalf("the private ranges need no list: %v, %v", got, err)
	}
}
