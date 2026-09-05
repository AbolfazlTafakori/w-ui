package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
)

// Producing the signature a panel checks before it installs an update.
//
// Ed25519 rather than a certificate authority: one key pair, two short strings,
// no expiry to manage and nothing to renew. The public half is compiled into
// every build and the private half never leaves the machine that makes
// releases — which is the whole security of the arrangement, and the reason
// this prints a warning rather than offering to store it anywhere.

func keygenCommand(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("keygen takes no arguments")
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	fmt.Println()
	fmt.Println("  A release-signing key pair.")
	fmt.Println()
	fmt.Println("  Build every release with the public half:")
	fmt.Println()
	fmt.Printf("    -ldflags \"-X github.com/abolfazl/w-ui/internal/update.PublicKey=%s\"\n",
		base64.StdEncoding.EncodeToString(pub))
	fmt.Println()
	fmt.Println("  Keep the private half somewhere only you can reach. Anyone holding it")
	fmt.Println("  can produce an update that every panel built with the matching public")
	fmt.Println("  key will install without question. It is not stored anywhere by this")
	fmt.Println("  command; if you lose it, you make a new pair and every old build stops")
	fmt.Println("  being able to update.")
	fmt.Println()
	fmt.Printf("    WUI_SIGNING_KEY=%s\n", base64.StdEncoding.EncodeToString(priv))
	fmt.Println()
	return nil
}

func signCommand(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("sign takes one file: wui sign <binary>")
	}

	raw := strings.TrimSpace(os.Getenv("WUI_SIGNING_KEY"))
	if raw == "" {
		return fmt.Errorf("set WUI_SIGNING_KEY to the private half from `wui keygen`")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("WUI_SIGNING_KEY is not the value keygen printed: %w", err)
	}
	if len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("WUI_SIGNING_KEY is %d bytes, not %d", len(key), ed25519.PrivateKeySize)
	}

	f, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer f.Close()

	// Read whole rather than streamed: ed25519 signs a message, not a digest,
	// and a panel binary is tens of megabytes rather than a size worth
	// engineering around.
	body, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read %s: %w", args[0], err)
	}

	sig := ed25519.Sign(ed25519.PrivateKey(key), body)
	out := args[0] + ".sig"
	if err := os.WriteFile(out, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}

	fmt.Printf("signed %s\n  -> %s\n", args[0], out)
	fmt.Println("\nPublish both as release assets. A panel downloads the pair and")
	fmt.Println("installs nothing if the signature does not match.")
	return nil
}
