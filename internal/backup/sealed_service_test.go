package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With a backup password every archive is sealed: named so, unreadable as
// gzip, listed as encrypted, and restored only with the password.
func TestWithAPasswordEveryArchiveIsSealed(t *testing.T) {
	s, _ := newService(t, 0)
	if err := s.SetPassword("short"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a five-character password: %v", err)
	}
	if err := s.SetPassword("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if !s.PasswordSet() {
		t.Fatal("the password is not reported as set")
	}
	a, err := s.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !a.Encrypted || !strings.HasSuffix(a.Name, ".tar.gz.enc") {
		t.Fatalf("archive %+v", a)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir(), a.Name))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "a server private key") || strings.Contains(string(raw), "user:secret") {
		t.Fatal("the sealed archive carries the keys in the clear")
	}
	list, _ := s.List()
	if len(list) != 1 || !list[0].Encrypted {
		t.Fatalf("listed %+v", list)
	}
	if _, err := s.Restore(context.Background(), a.Name, nil); err != nil {
		t.Fatalf("restore with the password: %v", err)
	}
	staged, err := os.ReadFile(filepath.Join(pendingDir(s.dataDir), "openvpn", "tun0", "server.key"))
	if err != nil || string(staged) != "a server private key" {
		t.Fatalf("restored %q: %v", staged, err)
	}
}

// A sealed archive brought to a panel with another password, or with none,
// is refused with what to do about it.
func TestASealedArchiveNeedsItsPassword(t *testing.T) {
	from, _ := newService(t, 0)
	if err := from.SetPassword("the first panel's password"); err != nil {
		t.Fatal(err)
	}
	a, err := from.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	upload := func(to *Service) error {
		f, err := os.Open(filepath.Join(from.Dir(), a.Name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		_, err = to.Accept(f)
		return err
	}
	none, _ := newService(t, 0)
	if err := upload(none); !errors.Is(err, ErrSealed) {
		t.Fatalf("no password: %v", err)
	}
	other, _ := newService(t, 0)
	if err := other.SetPassword("another panel's password"); err != nil {
		t.Fatal(err)
	}
	if err := upload(other); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("another password: %v", err)
	}
	same, _ := newService(t, 0)
	if err := same.SetPassword("the first panel's password"); err != nil {
		t.Fatal(err)
	}
	if err := upload(same); err != nil {
		t.Fatalf("the same password: %v", err)
	}
	list, _ := same.List()
	if len(list) != 1 || !list[0].Encrypted || !strings.HasSuffix(list[0].Name, "-uploaded.tar.gz.enc") {
		t.Fatalf("kept as %+v", list)
	}
}

// Taking the password away seals nothing from then on; archives sealed
// before keep their seal.
func TestRemovingThePasswordStopsSealing(t *testing.T) {
	s, _ := newService(t, 0)
	if err := s.SetPassword("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword(""); err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b.Encrypted || s.PasswordSet() {
		t.Fatalf("still sealing: %+v", b)
	}
}
