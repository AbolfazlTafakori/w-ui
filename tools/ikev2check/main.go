// Command ikev2check writes an IKEv2 tunnel as the panel would -- four
// devices, one per way of signing in -- and the files each device is given,
// for a check against a real strongSwan. Temporary: tools/protosim takes it
// over with the panel's own driver and agent.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/abolfazl/w-ui/internal/ikev2conf"
)

func main() {
	out := flag.String("out", "", "directory")
	flag.Parse()
	must := func(err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	ca, err := ikev2conf.NewAuthority("ike0")
	must(err)
	srv, err := ca.IssueServer([]string{"vpn.example.test", "172.31.99.10"})
	must(err)
	s := &ikev2conf.State{Version: ikev2conf.SchemaVersion, Interface: ikev2conf.Iface{
		ID: 1, Name: "ike0", Subnet: "10.79.0.0/24", Gateway: "10.79.0.1", DNS: []string{"1.1.1.1"},
		Modes:    []string{ikev2conf.ModeMSCHAPv2, ikev2conf.ModePSK, ikev2conf.ModeRSA, ikev2conf.ModeEAPTLS},
		ServerID: "vpn.example.test", CACert: ca.Cert, ServerCert: srv.Cert, ServerKey: srv.Key,
	}}
	for i := 1; i <= 4; i++ {
		u := fmt.Sprintf("s1-dev%02d", i)
		dev, err := ca.IssueDevice(u)
		must(err)
		secret := fmt.Sprintf("Pw%02dxk9Lq2", i)
		s.Interface.Accounts = append(s.Interface.Accounts, ikev2conf.Account{
			ID: uint(i), Username: u, Secret: secret, Cert: dev.Cert, IP: fmt.Sprintf("10.79.0.%d", i+1)})
		p12, err := ikev2conf.PKCS12(dev.Cert, dev.Key, ca.Cert, secret)
		must(err)
		must(os.WriteFile(filepath.Join(*out, u+".p12"), p12, 0o644))
		must(os.WriteFile(filepath.Join(*out, u+".crt"), []byte(dev.Cert), 0o644))
		must(os.WriteFile(filepath.Join(*out, u+".key"), []byte(dev.Key), 0o644))
	}
	must(s.Validate())
	for _, f := range ikev2conf.Files(s) {
		dir := filepath.Join(*out, "server", f.Dir)
		must(os.MkdirAll(dir, 0o755))
		must(os.WriteFile(filepath.Join(dir, f.Name), []byte(f.Body), 0o644))
	}
	must(os.WriteFile(filepath.Join(*out, "ca.crt"), []byte(ca.Cert), 0o644))
}
