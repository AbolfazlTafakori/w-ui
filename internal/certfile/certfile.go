// Package certfile serves a certificate from files that may be rewritten
// underneath it. A renewal that replaces the files is picked up on the
// next handshake, with no restart: a panel that had to be restarted to
// notice its own certificate would drop every tunnel it runs to do it.
package certfile

import (
	"crypto/tls"
	"os"
	"sync"
	"time"
)

// Loader hands out the certificate at a pair of paths, re-reading them
// when either changes.
type Loader struct {
	certPath, keyPath string

	mu      sync.Mutex
	cert    *tls.Certificate
	certMod time.Time
	keyMod  time.Time
	checked time.Time
}

// New reads the pair once, so a bad file is reported at start rather than
// on the first connection.
func New(certPath, keyPath string) (*Loader, error) {
	l := &Loader{certPath: certPath, keyPath: keyPath}
	if err := l.reload(); err != nil {
		return nil, err
	}
	return l, nil
}

// GetCertificate is what tls.Config.GetCertificate takes.
func (l *Loader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// The files are looked at once a minute at most; a stat per handshake
	// would be the busiest thing this server does.
	if time.Since(l.checked) > time.Minute {
		l.checked = time.Now()
		if l.changed() {
			// A renewal writes the certificate and the key one after the
			// other. Caught between the two, the pair does not match; the
			// old pair keeps serving until the next look finds a good one.
			_ = l.reload()
		}
	}
	return l.cert, nil
}

func (l *Loader) changed() bool {
	c, err1 := os.Stat(l.certPath)
	k, err2 := os.Stat(l.keyPath)
	if err1 != nil || err2 != nil {
		return false
	}
	return !c.ModTime().Equal(l.certMod) || !k.ModTime().Equal(l.keyMod)
}

func (l *Loader) reload() error {
	cert, err := tls.LoadX509KeyPair(l.certPath, l.keyPath)
	if err != nil {
		return err
	}
	c, _ := os.Stat(l.certPath)
	k, _ := os.Stat(l.keyPath)
	l.cert = &cert
	if c != nil {
		l.certMod = c.ModTime()
	}
	if k != nil {
		l.keyMod = k.ModTime()
	}
	l.checked = time.Now()
	return nil
}
