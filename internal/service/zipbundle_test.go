package service

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/abolfazl/w-ui/internal/backend"
)

func entries(t *testing.T, body []byte) map[string]string {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the bundle is not a readable zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatal(err)
		}
		rc.Close()
		out[f.Name] = buf.String()
	}
	return out
}

// A customer with a phone and a laptop has two WireGuard configurations, and a
// .conf file holds exactly one [Interface]. Joined into one file the second
// device silently does not exist, which is what every other format here does.
func TestZipBundleKeepsEveryDeviceSeparate(t *testing.T) {
	body, ctype := encodeBundle([]backend.ClientProfile{
		{Filename: "phone.conf", Body: []byte("[Interface]\nAddress = 10.0.0.2/32")},
		{Filename: "laptop.conf", Body: []byte("[Interface]\nAddress = 10.0.0.3/32")},
	}, "zip")

	if ctype != "application/zip" {
		t.Errorf("content type = %q, want application/zip", ctype)
	}

	files := entries(t, body)
	if len(files) != 2 {
		t.Fatalf("got %d files, want one per device: %v", len(files), files)
	}
	if !strings.Contains(files["phone.conf"], "10.0.0.2/32") {
		t.Errorf("phone.conf has the wrong body: %q", files["phone.conf"])
	}
	if !strings.Contains(files["laptop.conf"], "10.0.0.3/32") {
		t.Errorf("laptop.conf has the wrong body: %q", files["laptop.conf"])
	}
	for name, body := range files {
		if !strings.HasSuffix(body, "\n") {
			t.Errorf("%s does not end in a newline; some clients drop the last line", name)
		}
	}
}

// Two devices on different interfaces can arrive with the same filename. A zip
// with two identical names is one an unpacker either refuses or quietly reduces
// to a single file, losing the configuration this format exists to deliver.
func TestZipBundleMakesNamesUnique(t *testing.T) {
	files := entries(t, mustZip(t, []backend.ClientProfile{
		{Filename: "device.conf", Body: []byte("first")},
		{Filename: "device.conf", Body: []byte("second")},
		{Filename: "device-2.conf", Body: []byte("third")},
	}))

	if len(files) != 3 {
		t.Fatalf("got %d files, want 3 — a name collision lost one: %v", len(files), files)
	}
	seen := map[string]bool{}
	for _, want := range []string{"first", "second", "third"} {
		for _, got := range files {
			if strings.TrimSpace(got) == want {
				seen[want] = true
			}
		}
	}
	for _, want := range []string{"first", "second", "third"} {
		if !seen[want] {
			t.Errorf("the configuration %q is not in the archive: %v", want, files)
		}
	}
}

// A filename is chosen by a driver, but an entry with a path in it is how an
// unpacker gets talked into writing outside the directory it was pointed at.
func TestZipBundleRefusesPathsInNames(t *testing.T) {
	files := entries(t, mustZip(t, []backend.ClientProfile{
		{Filename: "../../etc/cron.d/root", Body: []byte("x")},
		{Filename: "/absolute/evil.conf", Body: []byte("y")},
		{Filename: "", Body: []byte("z")},
	}))

	for name := range files {
		if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
			t.Errorf("entry %q escapes its directory", name)
		}
	}
	if len(files) != 3 {
		t.Errorf("got %d entries, want 3 — a nameless profile should still be delivered: %v",
			len(files), files)
	}
}

// The other formats must keep behaving exactly as they did: a subscription URL
// returning an archive is one most clients reject outright.
func TestOtherFormatsAreStillText(t *testing.T) {
	parts := []backend.ClientProfile{
		{Filename: "a.conf", Body: []byte("one")},
		{Filename: "b.conf", Body: []byte("two")},
	}
	for _, format := range []string{"", "plain", "base64"} {
		_, ctype := encodeBundle(parts, format)
		if ctype != "text/plain; charset=utf-8" {
			t.Errorf("format %q gave content type %q, want text", format, ctype)
		}
	}
	body, _ := encodeBundle(parts, "")
	if string(body) != "one\n\ntwo" {
		t.Errorf("plain bundle = %q, want the configurations joined", body)
	}
	if got := configExt("zip"); got != ".zip" {
		t.Errorf("configExt(zip) = %q, want .zip", got)
	}
}

func mustZip(t *testing.T, parts []backend.ClientProfile) []byte {
	t.Helper()
	body, _ := encodeBundle(parts, "zip")
	if len(body) == 0 {
		t.Fatal("the zip came back empty")
	}
	return body
}
