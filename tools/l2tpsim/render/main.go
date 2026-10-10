// Command render writes the files the L2TP agent would write for a test
// tunnel, from the same renderers, so the simulation runs the server exactly
// as a customer's server runs it.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/abolfazl/w-ui/internal/l2tpconf"
)

func main() {
	out := flag.String("out", "", "directory to write into")
	psk := flag.String("psk", "", "the tunnel's pre-shared key")
	plain := flag.Bool("plain", false, "also accept L2TP without IPsec")
	pptp := flag.Bool("pptp", false, "a PPTP tunnel instead")
	flag.Parse()
	if *out == "" || (*psk == "" && !*pptp) {
		fmt.Fprintln(os.Stderr, "usage: render -out DIR (-psk KEY [-plain] | -pptp)")
		os.Exit(2)
	}
	if *pptp {
		write(*out, pptpFiles())
		return
	}
	s := &l2tpconf.State{Version: 1, Interface: l2tpconf.Iface{
		ID: 1, Name: "l2tp0",
		Subnet: "10.77.0.0/16", Gateway: "10.77.0.1",
		DNS: []string{"1.1.1.1"}, MTU: l2tpconf.DefaultMTU,
		PSK: *psk, AllowPlain: *plain,
		Accounts: accounts(),
	}}
	if err := s.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	files := map[string]string{
		"options":       l2tpconf.PPPOptions(s),
		"xl2tpd.conf":   l2tpconf.XL2TPD(s),
		"l2tp-secrets":  l2tpconf.TunnelSecrets(s),
		"chap-secrets":  l2tpconf.ChapSecrets("", s.Protocol, s),
		"wui-l2tp.conf": l2tpconf.Swanctl(s),
		"guard.nft":     l2tpconf.Guard(s),
	}
	write(*out, files)
}

// pptpFiles are the PPTP agent's files for a test tunnel: pptpd's own, the
// options its pppd runs with, and the sign-ins.
func pptpFiles() map[string]string {
	s := &l2tpconf.State{Version: 1, Protocol: l2tpconf.ProtocolPPTP, Interface: l2tpconf.Iface{
		ID: 2, Name: "pptp0",
		Subnet: "10.77.0.0/16", Gateway: "10.77.0.1",
		DNS: []string{"1.1.1.1"}, MTU: l2tpconf.DefaultMTU,
		Accounts: accounts(),
	}}
	if err := s.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return map[string]string{
		"pptpd.conf":   l2tpconf.PPTPD(s),
		"pptp-options": l2tpconf.PPTPOptions(s),
		"chap-secrets": l2tpconf.ChapSecrets("", s.Protocol, s),
	}
}

func write(dir string, files map[string]string) {
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

// accounts are one per client the simulation runs at once: every scenario
// signs in as its own device, on its own address.
func accounts() []l2tpconf.Account {
	var out []l2tpconf.Account
	for i := 1; i <= 40; i++ {
		out = append(out, l2tpconf.Account{
			ID:       uint(i),
			Username: fmt.Sprintf("dev%02d", i),
			Secret:   fmt.Sprintf("Pw%02dxk9Lq2", i),
			IP:       fmt.Sprintf("10.77.0.%d", i+1),
		})
	}
	return out
}
