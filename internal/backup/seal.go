package backup

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/scrypt"
)

// Sealing an archive with the operator's backup password.
//
// An archive is the database, the servers' keys and every customer's
// credentials, and it is sent where its operator sends it: Telegram most of
// all. Sealed, it is nothing to whoever else gets hold of it.
//
// The format, version 1, all of it written here:
//
//	magic       "WUIBAK1\n"                      8 bytes
//	salt        random, for scrypt               16 bytes
//	nonce base  random                           7 bytes
//	chunks      AES-256-GCM, each sealing up to sealChunk bytes of the
//	            archive, nonce = base || counter (4, big endian) || last (1)
//
// The key is scrypt(password, salt, N=2^15, r=8, p=1), 32 bytes. Each chunk
// carries its place (the counter) and whether it is the last, so a file cut
// short, with chunks reordered, dropped or changed, is refused rather than
// read as a shorter archive.

var sealMagic = []byte("WUIBAK1\n")

const (
	sealChunk     = 64 << 10
	sealSaltSize  = 16
	sealNonceBase = 7
	sealKeySize   = 32
)

// ErrSealed is an archive sealed with a password, opened without one.
var ErrSealed = errors.New("backup: this archive is encrypted; set its backup password under Settings → Backup first")

// ErrWrongPassword is a sealed archive the password given does not open, or
// one that was changed since it was sealed.
var ErrWrongPassword = errors.New("backup: the backup password does not open this archive, or the file was changed")

func sealKey(password string, salt []byte) (cipher.AEAD, error) {
	key, err := scrypt.Key([]byte(password), salt, 1<<15, 8, 1, sealKeySize)
	if err != nil {
		return nil, fmt.Errorf("backup: derive the key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealNonce(base []byte, counter uint32, last bool) []byte {
	n := make([]byte, 0, 12)
	n = append(n, base...)
	n = binary.BigEndian.AppendUint32(n, counter)
	if last {
		return append(n, 1)
	}
	return append(n, 0)
}

// sealer seals what is written to it, a chunk at a time.
type sealer struct {
	w       io.Writer
	aead    cipher.AEAD
	base    []byte
	counter uint32
	buf     []byte
	closed  bool
}

// newSealer writes the header to w and seals everything written after it.
// Close writes the last chunk; without it the file is not one.
func newSealer(w io.Writer, password string) (io.WriteCloser, error) {
	salt := make([]byte, sealSaltSize)
	base := make([]byte, sealNonceBase)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(base); err != nil {
		return nil, err
	}
	aead, err := sealKey(password, salt)
	if err != nil {
		return nil, err
	}
	head := append(append(append([]byte{}, sealMagic...), salt...), base...)
	if _, err := w.Write(head); err != nil {
		return nil, err
	}
	return &sealer{w: w, aead: aead, base: base, buf: make([]byte, 0, sealChunk)}, nil
}

func (s *sealer) Write(p []byte) (int, error) {
	if s.closed {
		return 0, errors.New("backup: write after close")
	}
	n := len(p)
	for len(p) > 0 {
		// A full chunk is sealed only when more follows, so the last one
		// is always sealed by Close, marked last.
		if len(s.buf) == sealChunk {
			if err := s.flush(false); err != nil {
				return n - len(p), err
			}
		}
		take := min(sealChunk-len(s.buf), len(p))
		s.buf = append(s.buf, p[:take]...)
		p = p[take:]
	}
	return n, nil
}

func (s *sealer) flush(last bool) error {
	if s.counter == ^uint32(0) {
		return errors.New("backup: archive too large to seal")
	}
	out := s.aead.Seal(nil, sealNonce(s.base, s.counter, last), s.buf, nil)
	s.counter++
	s.buf = s.buf[:0]
	_, err := s.w.Write(out)
	return err
}

func (s *sealer) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.flush(true)
}

// isSealed reports whether r begins as a sealed archive. It does not move
// a reader it is given: it peeks.
func isSealed(r *bufio.Reader) bool {
	head, err := r.Peek(len(sealMagic))
	return err == nil && bytes.Equal(head, sealMagic)
}

// opener reads a sealed archive back, refusing it at the first chunk that
// does not open in its place.
type opener struct {
	r       io.Reader
	aead    cipher.AEAD
	base    []byte
	counter uint32
	plain   []byte
	done    bool
	chunk   []byte
}

// openSealed reads the header from r and returns the archive inside.
// password "" is ErrSealed.
func openSealed(r io.Reader, password string) (io.Reader, error) {
	head := make([]byte, len(sealMagic)+sealSaltSize+sealNonceBase)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, fmt.Errorf("backup: the archive is too short to be one: %w", err)
	}
	if !bytes.Equal(head[:len(sealMagic)], sealMagic) {
		return nil, errors.New("backup: not an encrypted archive")
	}
	if password == "" {
		return nil, ErrSealed
	}
	salt := head[len(sealMagic) : len(sealMagic)+sealSaltSize]
	aead, err := sealKey(password, salt)
	if err != nil {
		return nil, err
	}
	o := &opener{r: r, aead: aead, base: head[len(sealMagic)+sealSaltSize:], chunk: make([]byte, sealChunk+aead.Overhead())}
	// The first chunk is opened now, so a wrong password is said here rather
	// than as a damaged gzip stream.
	if err := o.next(); err != nil {
		return nil, err
	}
	return o, nil
}

// next opens the following chunk into o.plain.
func (o *opener) next() error {
	n, err := io.ReadFull(o.r, o.chunk)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return err
	}
	if n < o.aead.Overhead() {
		// A file that ends where a chunk should be was cut short.
		return ErrWrongPassword
	}
	sealed := o.chunk[:n]
	// The last chunk says so in its nonce; a full chunk may be either.
	if plain, err := o.aead.Open(nil, sealNonce(o.base, o.counter, true), sealed, nil); err == nil {
		// Marked last: nothing may follow it.
		var extra [1]byte
		if m, _ := o.r.Read(extra[:]); m > 0 {
			return ErrWrongPassword
		}
		o.plain, o.done = plain, true
		o.counter++
		return nil
	}
	if n < sealChunk+o.aead.Overhead() {
		// Short and not the last: the end was cut off.
		return ErrWrongPassword
	}
	plain, err := o.aead.Open(nil, sealNonce(o.base, o.counter, false), sealed, nil)
	if err != nil {
		return ErrWrongPassword
	}
	o.plain = plain
	o.counter++
	return nil
}

func (o *opener) Read(p []byte) (int, error) {
	for len(o.plain) == 0 {
		if o.done {
			return 0, io.EOF
		}
		if err := o.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, o.plain)
	o.plain = o.plain[n:]
	return n, nil
}
