package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The hosts an update talks to: the release list, the downloads, and the
// scripts the installer and the menu fetch.
var githubHosts = []string{"api.github.com", "github.com", "raw.githubusercontent.com",
	"objects.githubusercontent.com", "codeload.github.com"}

// fakeGitHubCommand answers as GitHub does for one release, so an install can
// be updated -- from the panel's page, from the w-ui menu, by update.sh -- to
// a build signed with a test key, exactly as it would be to a real release.
//
// Nothing in the panel knows it is talking to this: CI points the GitHub
// names at this machine in /etc/hosts and trusts the authority written to
// -ca. The update code under test is the code that ships.
func fakeGitHubCommand(args []string) error {
	fs := flag.NewFlagSet("fakegithub", flag.ContinueOnError)
	addr := fs.String("addr", ":443", "where to listen")
	releaseDir := fs.String("release", "", "directory of release assets: wui-linux-amd64, its .sig, SHA256SUMS, ...")
	tag := fs.String("tag", "", "the release's tag, newer than the installed one")
	rawDir := fs.String("raw", "", "the source tree raw.githubusercontent.com serves, for any branch or tag")
	caOut := fs.String("ca", "", "where to write the authority's certificate, to be trusted")
	repo := fs.String("repo", "AbolfazlTafakori/w-ui", "owner/name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *releaseDir == "" || *tag == "" || *caOut == "" {
		return errors.New("fakegithub needs -release, -tag and -ca")
	}

	cert, caPEM, err := testAuthority(githubHosts)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*caOut, caPEM, 0o644); err != nil {
		return err
	}

	assets, err := os.ReadDir(*releaseDir)
	if err != nil {
		return err
	}
	release := func() map[string]any {
		var list []map[string]any
		for _, a := range assets {
			if a.IsDir() {
				continue
			}
			list = append(list, map[string]any{
				"name":                 a.Name(),
				"browser_download_url": fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", *repo, *tag, a.Name()),
			})
		}
		sort.Slice(list, func(i, j int) bool { return list[i]["name"].(string) < list[j]["name"].(string) })
		return map[string]any{"tag_name": *tag, "name": "W-UI " + strings.TrimPrefix(*tag, "v"),
			"body": "A test release.", "published_at": time.Now().UTC().Format(time.RFC3339), "assets": list}
	}

	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.Host)
		if host == "" {
			host = r.Host
		}
		log.Printf("%s %s%s", r.Method, host, r.URL.Path)
		switch host {
		case "api.github.com":
			p := strings.TrimPrefix(r.URL.Path, "/repos/"+*repo+"/releases/")
			if p == "latest" || p == "tags/"+*tag {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(release())
				return
			}
		case "github.com", "objects.githubusercontent.com":
			prefix := "/" + *repo + "/releases/download/" + *tag + "/"
			if name, ok := strings.CutPrefix(r.URL.Path, prefix); ok && !strings.Contains(name, "/") {
				http.ServeFile(w, r, filepath.Join(*releaseDir, name))
				return
			}
		case "raw.githubusercontent.com":
			// /owner/name/<ref>/<path>, for any ref.
			p, ok := strings.CutPrefix(r.URL.Path, "/"+*repo+"/")
			if _, rest, found := strings.Cut(p, "/"); ok && found && *rawDir != "" && !strings.Contains(rest, "..") {
				http.ServeFile(w, r, filepath.Join(*rawDir, filepath.FromSlash(rest)))
				return
			}
		}
		http.NotFound(w, r)
	})

	srv := &http.Server{
		Addr:      *addr,
		Handler:   mux,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
	}
	log.Printf("answering as GitHub for %s %s on %s", *repo, *tag, *addr)
	return srv.ListenAndServeTLS("", "")
}

// testAuthority makes a certificate authority that lives for this run and a
// certificate from it for hosts.
func testAuthority(hosts []string) (tls.Certificate, []byte, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "W-UI upgrade test authority"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	ca, _ := x509.ParseCertificate(caDER)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: hosts[0]},
		DNSNames:     hosts,
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	cert := tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: key}
	return cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), nil
}
