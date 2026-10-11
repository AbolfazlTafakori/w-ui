package wgconf

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
)

// AmneziaVPNKey is a configuration as the AmneziaVPN app takes it in a
// link or a QR code: "vpn://" and the text, compressed the way Qt's
// qCompress does it (the length, four bytes big-endian, then zlib) and
// written in URL-safe base64 without padding.
//
// The app decodes the key back to the text and reads it as it reads a
// .conf file, so what it imports is exactly the customer's file, I1-I5 and
// all, not a second rendering of it that could drift.
func AmneziaVPNKey(conf string) string {
	var b bytes.Buffer
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(conf)))
	b.Write(n[:])
	w, _ := zlib.NewWriterLevel(&b, zlib.BestCompression)
	_, _ = w.Write([]byte(conf))
	_ = w.Close()
	return "vpn://" + base64.RawURLEncoding.EncodeToString(b.Bytes())
}
