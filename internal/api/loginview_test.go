package api

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Signing in answers with the same operator the /me call does.
//
// The menu is drawn from the three questions the panel asks about a role.
// When sign-in answered with the bare record and /me answered with those
// flags, an owner was shown a reseller's menu -- two pages out of thirteen --
// from the moment they signed in until the first time they reloaded, because
// a missing flag reads as false. Whatever is added to one answer has to
// appear in the other, so this compares the shapes rather than a list of
// field names somebody has to remember to update.
func TestSignInAnswersWithTheSameOperatorAsMe(t *testing.T) {
	var login loginResponse
	adminField, ok := reflect.TypeOf(login).FieldByName("Admin")
	if !ok {
		t.Fatal("the sign-in answer has no admin on it")
	}

	meType := reflect.TypeOf((*adminView)(nil))
	if adminField.Type != meType {
		t.Fatalf("sign-in answers with %s, /me answers with %s: the panel would draw "+
			"one menu at sign-in and another on reload", adminField.Type, meType)
	}

	// And the flags the menu reads are really on it, under the names the
	// page looks for.
	fields := map[string]bool{}
	for i := range meType.Elem().NumField() {
		f := meType.Elem().Field(i)
		if tag := f.Tag.Get("json"); tag != "" {
			fields[tag] = true
		}
	}
	for _, want := range []string{"managesPanel", "managesAdmins", "seesEveryone", "twoFactor"} {
		if !fields[want] {
			t.Errorf("the operator the panel is sent has no %q on it", want)
		}
	}

	// A missing flag reads as false in the browser, which is what made this
	// worth pinning: the zero value of the answer is the most restricted menu.
	raw, err := json.Marshal(adminView{})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"managesPanel", "seesEveryone"} {
		if v, present := back[k]; !present || v != false {
			t.Errorf("%q is not sent as a plain false when unset: %v", k, v)
		}
	}
}
