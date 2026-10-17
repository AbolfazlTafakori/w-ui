package backup

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"testing"
)

func seal(t *testing.T, plain []byte, password string) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := newSealer(&out, password)
	if err != nil {
		t.Fatal(err)
	}
	// Written in odd pieces, as gzip writes.
	for p := plain; len(p) > 0; {
		n := min(len(p), 7777)
		if _, err := w.Write(p[:n]); err != nil {
			t.Fatal(err)
		}
		p = p[n:]
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func unseal(sealed []byte, password string) ([]byte, error) {
	r, err := openSealed(bytes.NewReader(sealed), password)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// What is sealed opens as it was, whatever its size against the chunks.
func TestASealedArchiveOpensAsItWas(t *testing.T) {
	for _, size := range []int{0, 1, sealChunk - 1, sealChunk, sealChunk + 1, 3*sealChunk + 12345} {
		plain := make([]byte, size)
		_, _ = rand.Read(plain)
		sealed := seal(t, plain, "correct horse")
		if bytes.Contains(sealed, plain[:min(size, 64)]) && size >= 64 {
			t.Fatalf("%d bytes: the archive is readable in the sealed file", size)
		}
		if !isSealed(bufio.NewReader(bytes.NewReader(sealed))) {
			t.Fatalf("%d bytes: not recognised as sealed", size)
		}
		got, err := unseal(sealed, "correct horse")
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("%d bytes: opened %d bytes, %v", size, len(got), err)
		}
	}
}

// The wrong password, no password, and a file changed, cut short, extended
// or reordered are each refused, never read as a different archive.
func TestASealedArchiveIsRefusedWhenItShouldBe(t *testing.T) {
	plain := make([]byte, 3*sealChunk+100)
	_, _ = rand.Read(plain)
	sealed := seal(t, plain, "correct horse")
	head := len(sealMagic) + sealSaltSize + sealNonceBase
	full := sealChunk + 16

	if _, err := unseal(sealed, "wrong horse"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := unseal(sealed, ""); !errors.Is(err, ErrSealed) {
		t.Errorf("no password: %v", err)
	}
	cases := map[string][]byte{
		"a byte changed":        flip(sealed, head+full+10),
		"cut at a chunk":        sealed[:head+2*full],
		"cut inside a chunk":    sealed[:len(sealed)-5],
		"the last chunk gone":   sealed[:head+3*full],
		"a byte added":          append(append([]byte{}, sealed...), 0),
		"two chunks swapped":    swap(sealed, head, full),
		"the header's salt off": flip(sealed, len(sealMagic)+1),
	}
	for name, bad := range cases {
		if got, err := unseal(bad, "correct horse"); err == nil {
			t.Errorf("%s: opened %d bytes", name, len(got))
		}
	}
}

func flip(b []byte, i int) []byte {
	out := append([]byte{}, b...)
	out[i] ^= 1
	return out
}

func swap(b []byte, head, full int) []byte {
	out := append([]byte{}, b...)
	a, c := out[head:head+full], out[head+full:head+2*full]
	tmp := append([]byte{}, a...)
	copy(a, c)
	copy(c, tmp)
	return out
}
