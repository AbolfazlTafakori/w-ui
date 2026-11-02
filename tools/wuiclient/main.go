//go:build linux

// Command wuiclient is a device's side of a stealth transport, for end-to-end
// tests against a real panel: it dials the gateway the way W-UI's own app does,
// opens a local UDP socket, and prints the address WireGuard is to send to. A
// kernel WireGuard interface pointed at that address then carries a real tunnel
// through the transport. It is a test tool, not the product's client.
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/abolfazl/w-ui/internal/stealth"
	"github.com/abolfazl/w-ui/internal/stealthgw"
)

type runner interface {
	Serve(context.Context) error
	LocalAddr() net.Addr
}

func main() {
	transport := flag.String("transport", "veil", "veil, anchor, drift, relay, mold, bare or mirror")
	server := flag.String("server", "", "veil/mirror: the gateway's host:port")
	url := flag.String("url", "", "anchor/drift/relay/mold/bare: the front's URL")
	secret := flag.String("secret", "", "the device's transport secret, base64")
	anchor := flag.String("anchor", "", "the tunnel's WireGuard public key, base64")
	key := flag.String("key", "", "the transport's public key, base64 (fronted transports)")
	cookie := flag.String("cookie", "", "bare: the cookie the token travels in")
	site := flag.String("site", "", "mirror: the borrowed site's name")
	nodekey := flag.String("nodekey", "", "mirror: the gateway's transport public key, base64")
	pin := flag.String("pin", "", "anchor: the gateway certificate's key pin, base64")
	listen := flag.String("listen", "127.0.0.1:0", "local address WireGuard sends to")
	mtu := flag.Int("mtu", 1420, "the tunnel's MTU")
	flag.Parse()

	sec, err := stealth.ParseSecret(*secret)
	if err != nil {
		log.Fatalf("secret: %v", err)
	}
	anc, err := stealth.ParseAnchor(*anchor)
	if err != nil {
		log.Fatalf("anchor: %v", err)
	}

	var c runner
	switch *transport {
	case "veil":
		c, err = stealthgw.NewClient(stealthgw.ClientConfig{
			Server: *server, Listen: *listen, Secret: sec, Anchor: anc, MTU: *mtu,
		})
	case "anchor", "drift", "bare":
		cfg := stealthgw.StreamClientConfig{
			Transport: *transport, URL: *url, Cookie: *cookie,
			Listen: *listen, Secret: sec, Anchor: anc, TransportKey: *key,
		}
		if *pin != "" {
			pp, perr := stealth.ParsePin(*pin)
			if perr != nil {
				log.Fatalf("pin: %v", perr)
			}
			cfg.Pins, cfg.SelfSigned = []stealth.Pin{pp}, true
		}
		c, err = stealthgw.NewStreamClient(cfg)
	case "relay":
		c, err = stealthgw.NewPollClient(stealthgw.PollClientConfig{
			URL: *url, Listen: *listen, Secret: sec, Anchor: anc, TransportKey: *key,
		})
	case "mold":
		sh, ok := stealth.Preset("get-poll")
		if !ok {
			log.Fatal("mold: the get-poll shape did not resolve")
		}
		c, err = stealthgw.NewShapeClient(stealthgw.ShapeClientConfig{
			URL: *url, Shape: sh, Listen: *listen, Secret: sec, Anchor: anc, TransportKey: *key,
		})
	case "mirror":
		if _, derr := base64.StdEncoding.DecodeString(*nodekey); derr != nil {
			log.Fatalf("nodekey: %v", derr)
		}
		c, err = stealthgw.NewFakeTLSClient(stealthgw.FakeTLSClientConfig{
			Dial: *server, Name: *site, NodeKey: *nodekey,
			Listen: *listen, Secret: sec, Anchor: anc,
		})
	default:
		log.Fatalf("unknown transport %q", *transport)
	}
	if err != nil {
		log.Fatalf("%s client: %v", *transport, err)
	}

	fmt.Printf("LISTEN %s\n", c.LocalAddr().String())
	os.Stdout.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := c.Serve(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("serve: %v", err)
	}
}
