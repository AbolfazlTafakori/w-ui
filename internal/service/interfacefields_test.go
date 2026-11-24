package service

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The interface form builds its request from a list of the fields the
// server takes (web/src/lib/interfaces/payload.test.js), because the server
// refuses any field it does not know: v3.2.0 sent the form's own awgMode
// and no interface could be made at all (AbolfazlTafakori/w-ui#14). That
// list and CreateInterfaceInput are the same set, so a field added, renamed
// or removed on either side fails here rather than on a customer's panel.
func TestTheInterfaceFormKnowsEveryFieldTheServerTakes(t *testing.T) {
	src, err := os.ReadFile("../../web/src/lib/interfaces/payload.test.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)const SERVER = new Set\(\[(.*?)\]\)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("payload.test.js has no SERVER set")
	}
	var ui []string
	for _, q := range regexp.MustCompile(`'(\w+)'`).FindAllSubmatch(m[1], -1) {
		ui = append(ui, string(q[1]))
	}
	var server []string
	ty := reflect.TypeOf(CreateInterfaceInput{})
	for i := 0; i < ty.NumField(); i++ {
		if name := strings.Split(ty.Field(i).Tag.Get("json"), ",")[0]; name != "" && name != "-" {
			server = append(server, name)
		}
	}
	sort.Strings(ui)
	sort.Strings(server)
	if strings.Join(ui, " ") != strings.Join(server, " ") {
		t.Fatalf("the form's list and the server's fields differ:\n  form:   %v\n  server: %v", ui, server)
	}
}
