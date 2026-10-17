package api

import (
	"encoding/json"
	"html"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	subPartRe = regexp.MustCompile(`(?s)<template id="part-(\d+)">(.*?)</template>`)
	subStubRe = regexp.MustCompile(`(?s)<(div|select)([^>]*?) data-from="(\d+)"(?: data-v="([^"]*)")?([^>]*)></(?:div|select)>`)
	subSlotRe = regexp.MustCompile(`@@(\d+)@@`)
)

// builtPage is a page as its script makes it before anything else runs:
// each stub given a copy of its part, with its values in the slots as text.
func builtPage(t *testing.T, page string) string {
	t.Helper()
	parts := map[string]string{}
	for _, m := range subPartRe.FindAllStringSubmatch(page, -1) {
		parts[m[1]] = m[2]
	}
	page = subPartRe.ReplaceAllString(page, "")
	return subStubRe.ReplaceAllStringFunc(page, func(stub string) string {
		m := subStubRe.FindStringSubmatch(stub)
		part, ok := parts[m[3]]
		if !ok {
			t.Fatalf("a stub names part %s, which the page does not have", m[3])
		}
		var vals []string
		if m[4] != "" {
			if err := json.Unmarshal([]byte(html.UnescapeString(m[4])), &vals); err != nil {
				t.Fatalf("a stub's values: %v", err)
			}
		}
		filled := subSlotRe.ReplaceAllStringFunc(part, func(slot string) string {
			n, _ := strconv.Atoi(strings.Trim(slot, "@"))
			if n >= len(vals) {
				t.Fatalf("part %s has slot %d, its stub %d values", m[3], n, len(vals))
			}
			return html.EscapeString(vals[n])
		})
		return "<" + m[1] + m[2] + m[5] + ">" + filled + "</" + m[1] + ">"
	})
}

// A part is kept once however often it is put, and a stub's values are
// carried as data, never as markup.
func TestAPartIsWrittenOnce(t *testing.T) {
	var s subShared
	a := s.put("deviceNotes", []setupDevice{{ID: "ios"}})
	if b := s.put("deviceNotes", []setupDevice{{ID: "ios"}}); a != b || len(s.Parts) != 1 {
		t.Fatalf("the same part twice: %d %d, %d kept", a, b, len(s.Parts))
	}
	if c := s.put("deviceNotes", []setupDevice{{ID: "android"}}); c == a || len(s.Parts) != 2 {
		t.Fatal("other data is another part")
	}
	if d := s.put("deviceList", []setupDevice{{ID: "ios"}}); d == a || len(s.Parts) != 3 {
		t.Fatal("another kind is another part")
	}
	if subSlots(nil) != "" {
		t.Fatal("a stub with no values carries none")
	}
	page := `<template id="part-0"><p title="` + subSlot(0) + `">` + subSlot(0) + `</p></template>` +
		`<div data-from="0" data-v="` + html.EscapeString(subSlots([]string{`<b>x</b>`})) + `"></div>`
	got := builtPage(t, page)
	if strings.Contains(got, "<b>") || !strings.Contains(got, `&lt;b&gt;x&lt;/b&gt;</p>`) {
		t.Fatalf("a value made markup: %s", got)
	}
}

// Every icon is drawn once, and each use points at it.
func TestAnIconIsDrawnOnce(t *testing.T) {
	for name, icon := range subIcons {
		if !strings.Contains(string(icon), `href="#i-`+name+`"`) || strings.Contains(string(icon), "<path") {
			t.Fatalf("%s: %s", name, icon)
		}
		if strings.Count(string(subSprite), `id="i-`+name+`"`) != 1 {
			t.Fatalf("%s is not in the sprite once", name)
		}
	}
}
