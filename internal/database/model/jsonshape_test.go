package model

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var updateShape = flag.Bool("update", false, "rewrite testdata/json-shape.golden")

// The tables are what the API answers with, so a column added, renamed or
// given another JSON name changes what every client of the API reads. That
// is allowed to happen only on purpose: this pins the JSON every model
// writes -- each field's name, its type, and whether it is left out when
// empty -- and a change fails here until the golden file is rewritten with
// -update and the change is looked at in review.
func TestTheJSONOfEveryTableIsDeliberate(t *testing.T) {
	var b strings.Builder
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(rt reflect.Type) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Map {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] || rt.PkgPath() != reflect.TypeOf(Node{}).PkgPath() {
			return
		}
		seen[rt] = true
		fmt.Fprintf(&b, "%s\n", rt.Name())
		var nested []reflect.Type
		for i := range rt.NumField() {
			f := rt.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			if f.Anonymous && name == "" {
				fmt.Fprintf(&b, "\t(embeds %s)\n", f.Type.Name())
				nested = append(nested, f.Type)
				continue
			}
			if name == "" {
				name = f.Name
			}
			line := fmt.Sprintf("\t%s %s", name, f.Type.String())
			if strings.Contains(opts, "omitempty") {
				line += " omitempty"
			}
			if strings.Contains(opts, "omitzero") {
				line += " omitzero"
			}
			if f.Tag.Get("gorm") == "-" {
				line += " (not stored)"
			}
			fmt.Fprintln(&b, line)
			nested = append(nested, f.Type)
		}
		for _, n := range nested {
			walk(n)
		}
	}
	for _, m := range AllModels() {
		walk(reflect.TypeOf(m))
	}
	got := b.String()

	path := filepath.Join("testdata", "json-shape.golden")
	if *updateShape {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; run go test ./internal/database/model -update to write it", err)
	}
	if string(want) != got {
		t.Errorf("the JSON the tables answer with changed; if that is meant, run\n"+
			"  go test ./internal/database/model -run TestTheJSONOfEveryTableIsDeliberate -update\n"+
			"and check the API reference and the contract goldens.\n--- want\n%s\n--- got\n%s", want, got)
	}
}
