package service

import (
	"reflect"
	"testing"
)

// The addresses the subscription service keeps answering after it moves.
func TestTheAddressesAServiceLeavesAreKept(t *testing.T) {
	plain2087 := SubSettings{Enabled: true, Path: "/subscribe/", Port: 2087}
	tls44600 := SubSettings{Enabled: true, Path: "/subscribe/", Port: 44600, CertFile: "c", KeyFile: "k"}
	onPanel := SubSettings{Enabled: true, Path: "/sub/"}

	// Moved from a plain port to an HTTPS one: the plain one kept, as it was.
	if got := formerAfter(plain2087, tls44600, nil); !reflect.DeepEqual(got, []SubAddress{{Port: 2087, Path: "/subscribe/"}}) {
		t.Fatalf("2087 to 44600: %+v", got)
	}
	// From the panel's port, with another path: the panel's port, with its path.
	if got := formerAfter(onPanel, tls44600, nil); !reflect.DeepEqual(got, []SubAddress{{Port: 0, Path: "/sub/"}}) {
		t.Fatalf("panel to 44600: %+v", got)
	}
	// Saved with nothing moved: nothing added.
	if got := formerAfter(tls44600, tls44600, nil); len(got) != 0 {
		t.Fatalf("unchanged: %+v", got)
	}
	// Back where it was: that one is the service's again, not a former one.
	if got := formerAfter(tls44600, plain2087, []SubAddress{{Port: 2087, Path: "/subscribe/"}}); !reflect.DeepEqual(got, []SubAddress{{Port: 44600, TLS: true, Path: "/subscribe/"}}) {
		t.Fatalf("back to 2087: %+v", got)
	}
	// One the operator names, kept; a twin of it, once; no more than five.
	listed := []SubAddress{{Port: 8080, Path: "sub"}, {Port: 8080, Path: "/sub/"}}
	for p := 9000; p < 9010; p++ {
		listed = append(listed, SubAddress{Port: p, Path: "/feed/"})
	}
	got := formerAfter(tls44600, tls44600, listed)
	if len(got) != maxFormerSub || got[0] != (SubAddress{Port: 8080, Path: "/sub/"}) || got[1].Port != 9000 {
		t.Fatalf("listed: %+v", got)
	}
	// A disabled service left nowhere that anyone was handed.
	if got := formerAfter(SubSettings{Path: "/x/", Port: 1}, tls44600, nil); len(got) != 0 {
		t.Fatalf("from disabled: %+v", got)
	}
}
