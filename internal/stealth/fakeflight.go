package stealth

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"time"
)

// The wui-faketls handshake flight.
//
// When a hello proves a device, the gateway answers with its own TLS 1.3
// handshake, written here (not by any TLS library, so its records can be
// shaped after the site's). The server sends, after a plaintext ServerHello:
// EncryptedExtensions, a Certificate carrying the throwaway leaf, a
// CertificateVerify signed by the leaf, and Finished -- all under the
// handshake keys. The client checks the leaf against the one its own key
// makes (FakeClient.Leaf) and against nothing else, verifies the signature
// and the server's Finished, and answers with its own Finished. Both sides
// then hold the same application traffic keys.
//
// This file is the assembly and the key exchange for the X25519 group, with
// no sockets and no shaping: the gateway shapes the flight's bytes into
// records of the site's sizes, and carries the session's data as
// `[len u16 | chunk plaintext]` inside application records it protects with
// these keys. The hybrid X25519MLKEM768 group is a later addition.

// TLS content types and handshake message types.
const (
	tlsHandshake   = 22
	tlsAppData     = 23
	tlsChangeCS    = 20
	hsEncryptedExt = 8
	hsCertificate  = 11
	hsCertVerify   = 15
	hsFinished     = 20
	// sigECDSAP256 is ecdsa_secp256r1_sha256.
	sigECDSAP256 = 0x0403
)

var certVerifyLabel = []byte("TLS 1.3, server CertificateVerify")

// hsMsg wraps a handshake body in its 4-byte header.
func hsMsg(kind byte, body []byte) []byte {
	m := []byte{kind, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	return append(m, body...)
}

func hsHash(s Suite, transcript []byte) []byte {
	h := s.newHash()
	h.Write(transcript)
	return h.Sum(nil)
}

// leafCert is a minimal self-signed certificate carrying the leaf's public
// key. The client reads only the key from it.
func leafCert(leaf *ecdsa.PrivateKey) ([]byte, error) {
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Unix(1600000000, 0),
		NotAfter:     time.Unix(1600000000+86400*365, 0),
	}
	return x509.CreateCertificate(rand.Reader, tmpl, tmpl, &leaf.PublicKey, leaf)
}

// certMessage is the Certificate handshake message for one DER certificate.
func certMessage(der []byte) []byte {
	entry := []byte{byte(len(der) >> 16), byte(len(der) >> 8), byte(len(der))}
	entry = append(entry, der...)
	entry = append(entry, 0, 0) // no certificate extensions
	body := []byte{0}           // empty certificate_request_context
	listLen := len(entry)
	body = append(body, byte(listLen>>16), byte(listLen>>8), byte(listLen))
	body = append(body, entry...)
	return hsMsg(hsCertificate, body)
}

// certVerifyContent is the bytes a CertificateVerify signs.
func certVerifyContent(transcriptHash []byte) []byte {
	c := bytes.Repeat([]byte{0x20}, 64)
	c = append(c, certVerifyLabel...)
	c = append(c, 0)
	return append(c, transcriptHash...)
}

// FakeConn is the two directions of a wui-faketls session's record layer,
// after the handshake: application traffic keys the data rides under.
type FakeConn struct {
	Send *TLSKeys // what this side seals
	Recv *TLSKeys // what this side opens
}

// FakeServerSide is the server mid-handshake: the flight to send, and what it
// needs to take the client's Finished.
type FakeServerSide struct {
	// Flight is the bytes to send: the ServerHello record, a compatibility
	// change_cipher_spec, and the encrypted handshake records.
	Flight []byte

	suite     Suite
	hsClient  *TLSKeys
	finished  []byte // the client Finished verify data expected
	clientApp []byte
	serverApp []byte
}

// FakeServerFlight builds the server's flight for a hello that proved a
// device. suite is the suite the gateway chose from the hello, and leaf the
// throwaway key from the proof's seed. It answers with the X25519 group,
// using the hello's X25519 share.
func FakeServerFlight(clientHello []byte, h Hello, suite Suite, leaf *ecdsa.PrivateKey) (*FakeServerSide, error) {
	if h.Share+32 > len(clientHello) {
		return nil, errors.New("stealth: hello has no share")
	}
	shared, serverKey, group, err := serverKEX(clientHello, h)
	if err != nil {
		return nil, err
	}

	session := clientHello[fakeSessionAt : fakeSessionAt+32]
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	sh := WriteServerHello(session, suite, group, false, random, serverKey)

	tr := append([]byte(nil), clientHello...)
	tr = append(tr, sh...)
	hs := HandshakeSecrets(suite, shared, hsHash(suite, tr))
	serverHS, err := NewTLSKeys(suite, hs.Server)
	if err != nil {
		return nil, err
	}
	clientHS, err := NewTLSKeys(suite, hs.Client)
	if err != nil {
		return nil, err
	}

	ee := hsMsg(hsEncryptedExt, []byte{0, 0})
	der, err := leafCert(leaf)
	if err != nil {
		return nil, err
	}
	cert := certMessage(der)
	tr = append(tr, ee...)
	tr = append(tr, cert...)
	sig, err := ecdsa.SignASN1(rand.Reader, leaf, sha256Of(certVerifyContent(hsHash(suite, tr))))
	if err != nil {
		return nil, err
	}
	cv := hsMsg(hsCertVerify, append([]byte{sigECDSAP256 >> 8, sigECDSAP256 & 0xff, byte(len(sig) >> 8), byte(len(sig))}, sig...))
	tr = append(tr, cv...)
	serverFin := hsMsg(hsFinished, Finished(suite, hs.Server, hsHash(suite, tr)))
	tr = append(tr, serverFin...)

	clientApp, serverApp := ApplicationSecrets(suite, hs.Next, hsHash(suite, tr))

	// The flight on the wire.
	flight := make([]byte, 0, len(sh)+8+len(ee)+len(cert)+len(cv)+len(serverFin)+64)
	flight = recordPlain(flight, tlsHandshake, sh)
	flight = append(flight, tlsChangeCS, 3, 3, 0, 1, 1) // compatibility CCS
	inner := append(append(append(append([]byte(nil), ee...), cert...), cv...), serverFin...)
	sealed, ok := serverHS.Seal(tlsHandshake, inner, 0, nil)
	if !ok {
		return nil, errors.New("stealth: the flight does not fit one record")
	}
	flight = append(flight, sealed...)

	// What takes the client's Finished: the client handshake keys, and the
	// verify data expected over the transcript through the server Finished.
	clientFin := Finished(suite, hs.Client, hsHash(suite, tr))
	return &FakeServerSide{
		Flight: flight, suite: suite, hsClient: clientHS,
		finished: clientFin, clientApp: clientApp, serverApp: serverApp,
	}, nil
}

// AcceptClientFinished opens the client's Finished record and checks it. On
// success it returns the server's record layer for application data.
func (s *FakeServerSide) AcceptClientFinished(record []byte) (*FakeConn, error) {
	kind, msg, ok := openRecord(s.hsClient, record)
	if !ok || kind != tlsHandshake || len(msg) < 4 || msg[0] != hsFinished {
		return nil, errors.New("stealth: the client Finished did not open")
	}
	if !hmacEqual(msg[4:], s.finished) {
		return nil, errors.New("stealth: the client Finished is wrong")
	}
	send, err := NewTLSKeys(s.suite, s.serverApp)
	if err != nil {
		return nil, err
	}
	recv, err := NewTLSKeys(s.suite, s.clientApp)
	if err != nil {
		return nil, err
	}
	return &FakeConn{Send: send, Recv: recv}, nil
}

// FakeClientFlight reads the server's flight, checks the leaf against expected
// and the server's Finished, and returns the client's Finished record to send
// and the client's record layer for application data. x is the X25519 scalar
// behind the hello's share, expected the leaf FakeClient.Leaf gives. mlkemSeed
// is the seed of the ML-KEM decapsulation key behind a hybrid hello, and nil
// for a standalone X25519 one.
func FakeClientFlight(clientHello, x, mlkemSeed, expected, flight []byte) ([]byte, *FakeConn, error) {
	rd := &recordReader{b: flight}
	// ServerHello, in a plaintext handshake record.
	kind, sh, ok := rd.plain()
	if !ok || kind != tlsHandshake {
		return nil, nil, errors.New("stealth: no ServerHello record")
	}
	session := clientHello[fakeSessionAt : fakeSessionAt+32]
	hello, ok := ReadServerHello(sh, session)
	if !ok || (hello.Group != GroupX25519 && hello.Group != GroupHybrid) {
		return nil, nil, errors.New("stealth: the ServerHello is not one we take")
	}
	suite := hello.Suite
	shared, err := clientKEX(hello.Group, hello.Key, x, mlkemSeed)
	if err != nil {
		return nil, nil, err
	}

	tr := append([]byte(nil), clientHello...)
	tr = append(tr, sh...)
	hs := HandshakeSecrets(suite, shared, hsHash(suite, tr))
	serverHS, err := NewTLSKeys(suite, hs.Server)
	if err != nil {
		return nil, nil, err
	}
	clientHS, err := NewTLSKeys(suite, hs.Client)
	if err != nil {
		return nil, nil, err
	}

	// Skip a compatibility change_cipher_spec, then open the handshake flight.
	rd.skipChangeCipherSpec()
	kind, inner, ok := rd.encrypted(serverHS)
	if !ok || kind != tlsHandshake {
		return nil, nil, errors.New("stealth: the handshake flight did not open")
	}
	msgs, ok := splitHandshake(inner)
	if !ok || len(msgs) != 4 || msgs[0][0] != hsEncryptedExt || msgs[1][0] != hsCertificate || msgs[2][0] != hsCertVerify || msgs[3][0] != hsFinished {
		return nil, nil, errors.New("stealth: the flight is not the four messages")
	}
	ee, cert, cv, serverFin := msgs[0], msgs[1], msgs[2], msgs[3]

	leafPub, ok := certLeaf(cert)
	if !ok || !bytes.Equal(leafPub, expected) {
		return nil, nil, errors.New("stealth: the leaf is not the one the proof makes")
	}
	tr = append(tr, ee...)
	tr = append(tr, cert...)
	if !verifyCertVerify(cv, leafPub, hsHash(suite, tr)) {
		return nil, nil, errors.New("stealth: the CertificateVerify does not verify")
	}
	tr = append(tr, cv...)
	if !hmacEqual(serverFin[4:], Finished(suite, hs.Server, hsHash(suite, tr))) {
		return nil, nil, errors.New("stealth: the server Finished is wrong")
	}
	tr = append(tr, serverFin...)

	clientApp, serverApp := ApplicationSecrets(suite, hs.Next, hsHash(suite, tr))
	clientFin := hsMsg(hsFinished, Finished(suite, hs.Client, hsHash(suite, tr)))
	record, ok := clientHS.Seal(tlsHandshake, clientFin, 0, nil)
	if !ok {
		return nil, nil, errors.New("stealth: the client Finished did not seal")
	}
	send, err := NewTLSKeys(suite, clientApp)
	if err != nil {
		return nil, nil, err
	}
	recv, err := NewTLSKeys(suite, serverApp)
	if err != nil {
		return nil, nil, err
	}
	return record, &FakeConn{Send: send, Recv: recv}, nil
}

// serverKEX does the server's half of the key exchange for a hello of group
// h.Group. For X25519 it is one ECDH; for the hybrid group it also
// encapsulates against the client's ML-KEM key, and the shared secret is the
// ML-KEM secret followed by the X25519 one, as the hybrid group defines. It
// returns the shared secret, the key bytes the ServerHello carries, and the
// group answered.
func serverKEX(clientHello []byte, h Hello) (shared, serverKey []byte, group uint16, err error) {
	serverEph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, 0, err
	}
	peer, err := ecdh.X25519().NewPublicKey(clientHello[h.Share : h.Share+32])
	if err != nil {
		return nil, nil, 0, err
	}
	ssX, err := serverEph.ECDH(peer)
	if err != nil {
		return nil, nil, 0, errors.New("stealth: the client share is low order")
	}
	if h.Group != GroupHybrid {
		return ssX, serverEph.PublicKey().Bytes(), GroupX25519, nil
	}
	start := h.Share - mlkem.EncapsulationKeySize768
	if start < 0 {
		return nil, nil, 0, errors.New("stealth: the hybrid share is short")
	}
	ek, err := mlkem.NewEncapsulationKey768(clientHello[start:h.Share])
	if err != nil {
		return nil, nil, 0, err
	}
	ssK, ct := ek.Encapsulate()
	shared = append(append([]byte(nil), ssK...), ssX...)
	serverKey = append(append([]byte(nil), ct...), serverEph.PublicKey().Bytes()...)
	return shared, serverKey, GroupHybrid, nil
}

// clientKEX does the client's half: X25519 against the server's share, and for
// the hybrid group an ML-KEM decapsulation of the server's ciphertext first.
// serverKey is the ServerHello's key bytes, mlkemSeed the client's ML-KEM
// decapsulation seed (nil for the standalone group).
func clientKEX(group uint16, serverKey, x, mlkemSeed []byte) ([]byte, error) {
	priv, err := ecdh.X25519().NewPrivateKey(x)
	if err != nil {
		return nil, err
	}
	if group != GroupHybrid {
		peer, err := ecdh.X25519().NewPublicKey(serverKey)
		if err != nil {
			return nil, err
		}
		ssX, err := priv.ECDH(peer)
		if err != nil {
			return nil, errors.New("stealth: the server share is low order")
		}
		return ssX, nil
	}
	if len(serverKey) != mlkem.CiphertextSize768+32 {
		return nil, errors.New("stealth: the hybrid server share is the wrong length")
	}
	ct, serverX := serverKey[:mlkem.CiphertextSize768], serverKey[mlkem.CiphertextSize768:]
	dk, err := mlkem.NewDecapsulationKey768(mlkemSeed)
	if err != nil {
		return nil, err
	}
	ssK, err := dk.Decapsulate(ct)
	if err != nil {
		return nil, errors.New("stealth: the server ciphertext did not decapsulate")
	}
	peer, err := ecdh.X25519().NewPublicKey(serverX)
	if err != nil {
		return nil, err
	}
	ssX, err := priv.ECDH(peer)
	if err != nil {
		return nil, errors.New("stealth: the server share is low order")
	}
	return append(append([]byte(nil), ssK...), ssX...), nil
}

// --- record and message helpers ---

func sha256Of(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

func hmacEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// recordPlain appends an unprotected record of content type kind.
func recordPlain(out []byte, kind byte, body []byte) []byte {
	out = append(out, kind, 3, 3, byte(len(body)>>8), byte(len(body)))
	return append(out, body...)
}

// openRecord opens a single protected record (header + body) under keys.
func openRecord(keys *TLSKeys, record []byte) (byte, []byte, bool) {
	if len(record) < TLSHeader {
		return 0, nil, false
	}
	length := int(record[3])<<8 | int(record[4])
	if len(record) != TLSHeader+length {
		return 0, nil, false
	}
	header := record[:TLSHeader]
	body := append([]byte(nil), record[TLSHeader:]...)
	return keys.Open(header, body)
}

// recordReader reads TLS records from a flight.
type recordReader struct{ b []byte }

func (r *recordReader) next() (kind byte, body []byte, ok bool) {
	if len(r.b) < TLSHeader {
		return 0, nil, false
	}
	length := int(r.b[3])<<8 | int(r.b[4])
	if len(r.b) < TLSHeader+length {
		return 0, nil, false
	}
	kind = r.b[0]
	body = r.b[TLSHeader : TLSHeader+length]
	r.b = r.b[TLSHeader+length:]
	return kind, body, true
}

// plain returns the next record's content and type as plaintext.
func (r *recordReader) plain() (byte, []byte, bool) {
	kind, body, ok := r.next()
	return kind, body, ok
}

func (r *recordReader) skipChangeCipherSpec() {
	for len(r.b) >= TLSHeader && r.b[0] == tlsChangeCS {
		length := int(r.b[3])<<8 | int(r.b[4])
		if len(r.b) < TLSHeader+length {
			return
		}
		r.b = r.b[TLSHeader+length:]
	}
}

func (r *recordReader) encrypted(keys *TLSKeys) (byte, []byte, bool) {
	if len(r.b) < TLSHeader {
		return 0, nil, false
	}
	length := int(r.b[3])<<8 | int(r.b[4])
	if len(r.b) < TLSHeader+length {
		return 0, nil, false
	}
	header := r.b[:TLSHeader]
	body := append([]byte(nil), r.b[TLSHeader:TLSHeader+length]...)
	r.b = r.b[TLSHeader+length:]
	return keys.Open(header, body)
}

// splitHandshake splits a run of handshake messages by their length headers.
func splitHandshake(b []byte) ([][]byte, bool) {
	var out [][]byte
	for len(b) > 0 {
		if len(b) < 4 {
			return nil, false
		}
		length := int(b[1])<<16 | int(b[2])<<8 | int(b[3])
		if 4+length > len(b) {
			return nil, false
		}
		out = append(out, b[:4+length])
		b = b[4+length:]
	}
	return out, true
}

// certLeaf reads the leaf certificate's public key from a Certificate
// message, in the uncompressed SEC1 form.
func certLeaf(message []byte) ([]byte, bool) {
	if len(message) < 4 || message[0] != hsCertificate {
		return nil, false
	}
	body := message[4:]
	if len(body) < 1 {
		return nil, false
	}
	ctxLen := int(body[0])
	body = body[1:]
	if len(body) < ctxLen+3 {
		return nil, false
	}
	body = body[ctxLen:]
	listLen := int(body[0])<<16 | int(body[1])<<8 | int(body[2])
	body = body[3:]
	if listLen > len(body) || listLen < 3 {
		return nil, false
	}
	certLen := int(body[0])<<16 | int(body[1])<<8 | int(body[2])
	body = body[3:]
	if certLen > len(body) {
		return nil, false
	}
	der := body[:certLen]
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, false
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, false
	}
	key, err := pub.ECDH()
	if err != nil {
		return nil, false
	}
	return key.Bytes(), true
}

// verifyCertVerify checks a CertificateVerify message's signature against the
// leaf's public key and the transcript hash.
func verifyCertVerify(message, leafPub, transcriptHash []byte) bool {
	if len(message) < 4 || message[0] != hsCertVerify {
		return false
	}
	body := message[4:]
	if len(body) < 4 {
		return false
	}
	scheme := int(body[0])<<8 | int(body[1])
	sigLen := int(body[2])<<8 | int(body[3])
	if scheme != sigECDSAP256 || 4+sigLen != len(body) {
		return false
	}
	sig := body[4:]
	key, err := ecdh.P256().NewPublicKey(leafPub)
	if err != nil {
		return false
	}
	x, y := new(big.Int).SetBytes(key.Bytes()[1:33]), new(big.Int).SetBytes(key.Bytes()[33:65])
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	return ecdsa.VerifyASN1(pub, sha256Of(certVerifyContent(transcriptHash)), sig)
}
