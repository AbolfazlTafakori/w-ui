package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"time"

	"github.com/abolfazl/w-ui/internal/upgradecheck"
)

// nodesCommand checks that a managing panel and a node of different releases
// still work together: the panel registers the node with a token the node
// issued, puts a tunnel and a customer on it, and the node ends up running
// both -- then the customer is removed and the node lets them go. Both
// panels must already be running; scripts/upgrade/node-compat.sh starts them.
func nodesCommand(args []string) error {
	fs := flag.NewFlagSet("nodes", flag.ContinueOnError)
	panelURL := fs.String("panel", "", "the managing panel")
	nodeURL := fs.String("node", "", "the node, as the panel reaches it")
	user := fs.String("user", "", "administrator, the same on both")
	pass := fs.String("pass", "", "password")
	within := fs.Duration("within", 2*time.Minute, "how long the node has to catch up")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *panelURL == "" || *nodeURL == "" {
		return errors.New("nodes needs -panel and -node")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	for _, u := range []string{*panelURL, *nodeURL} {
		if err := upgradecheck.WaitUp(ctx, u, time.Minute); err != nil {
			return err
		}
	}
	panel, err := upgradecheck.Login(ctx, *panelURL, *user, *pass)
	if err != nil {
		return fmt.Errorf("the panel: %w", err)
	}
	node, err := upgradecheck.Login(ctx, *nodeURL, *user, *pass)
	if err != nil {
		return fmt.Errorf("the node: %w", err)
	}
	panelVersion, nodeVersion := versionOf(ctx, panel), versionOf(ctx, node)
	fmt.Printf("panel %s, node %s\n", panelVersion, nodeVersion)

	// The node issues a token; the panel registers it with it.
	var issued struct {
		Token string `json:"token"`
	}
	if err := node.Do(ctx, http.MethodPost, "api/tokens", map[string]string{"name": "managing panel"}, &issued); err != nil {
		return fmt.Errorf("the node issuing a token: %w", err)
	}
	allow := true
	var registered struct {
		ID uint `json:"id"`
	}
	if err := panel.Do(ctx, http.MethodPost, "api/nodes", map[string]any{
		"name": "ci-node", "address": *nodeURL, "token": issued.Token, "allowPrivateAddress": allow,
	}, &registered); err != nil {
		return fmt.Errorf("the panel registering the node: %w", err)
	}
	step := func(what string, ok func() (bool, error)) error {
		deadline := time.Now().Add(*within)
		for {
			done, err := ok()
			if done {
				fmt.Println("  ok:", what)
				return nil
			}
			if time.Now().After(deadline) {
				if err != nil {
					return fmt.Errorf("%s: %w", what, err)
				}
				return fmt.Errorf("%s: not within %s", what, *within)
			}
			time.Sleep(2 * time.Second)
		}
	}

	if err := step("the panel reaches the node", func() (bool, error) {
		var list []struct {
			ID        uint   `json:"id"`
			Reachable bool   `json:"reachable"`
			LastError string `json:"lastError"`
		}
		if err := panel.Do(ctx, http.MethodGet, "api/nodes", nil, &list); err != nil {
			return false, err
		}
		for _, n := range list {
			if n.ID == registered.ID {
				if n.LastError != "" {
					return n.Reachable, errors.New(n.LastError)
				}
				return n.Reachable, nil
			}
		}
		return false, errors.New("the node is not listed")
	}); err != nil {
		return err
	}

	// A tunnel and a customer on it, made on the panel.
	var iface struct {
		Interface struct {
			ID uint `json:"id"`
		} `json:"interface"`
	}
	if err := panel.Do(ctx, http.MethodPost, "api/interfaces", map[string]any{
		"name": "ci-node-wg", "protocol": "wireguard", "mode": "standard", "listenPort": 51931,
		"subnet": "10.79.0.0/24", "endpointHost": "node.example.test", "nodeId": registered.ID,
	}, &iface); err != nil {
		return fmt.Errorf("a tunnel on the node: %w", err)
	}
	var customer struct {
		ID uint `json:"id"`
	}
	if err := panel.Do(ctx, http.MethodPost, "api/clients", map[string]any{
		"name": "on-the-node", "interfaceId": iface.Interface.ID, "interfaceIds": []uint{iface.Interface.ID},
		"quotaBytes": 1 << 30,
	}, &customer); err != nil {
		return fmt.Errorf("a customer on the node: %w", err)
	}

	// The node holds the customer under the panel's id for them, not their
	// name: a node is told only what it needs to run the tunnel.
	onNode := func() (bool, error) {
		m := &upgradecheck.Manifest{}
		if err := upgradecheck.Describe(ctx, node, m); err != nil {
			return false, err
		}
		for _, cu := range m.Clients {
			if cu.OriginID == customer.ID {
				return true, nil
			}
		}
		return false, nil
	}
	if err := step("the node runs the tunnel", func() (bool, error) {
		var list []struct {
			Name string `json:"name"`
		}
		if err := node.Do(ctx, http.MethodGet, "api/interfaces", nil, &list); err != nil {
			return false, err
		}
		for _, i := range list {
			if i.Name == "ci-node-wg" {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return err
	}
	if err := step("the node has the customer", func() (bool, error) {
		return onNode()
	}); err != nil {
		return err
	}

	// The customer's link, from the panel, serves a configuration for the
	// node's tunnel.
	if err := upgradecheck.SetUpSubscription(ctx, panel, 0, nil); err != nil {
		return err
	}
	var link struct {
		Link string `json:"link"`
	}
	if err := panel.Do(ctx, http.MethodGet, fmt.Sprintf("api/clients/%d/subscription", customer.ID), nil, &link); err != nil {
		return err
	}
	got, err := upgradecheck.FetchSubscription(ctx, link.Link)
	if err != nil {
		return err
	}
	ps, err := upgradecheck.ParseProfiles(got.Body)
	if err != nil || len(ps) != 1 || ps[0].Validate() != nil || ps[0].Keys["peer.endpoint"] != "node.example.test:51931" {
		return fmt.Errorf("the customer's link on the node's tunnel serves %d configurations (%v): %s", len(ps), err, got.Body)
	}
	fmt.Println("  ok: the customer's link serves the node's tunnel")

	// And let go.
	if err := panel.Do(ctx, http.MethodDelete, fmt.Sprintf("api/clients/%d", customer.ID), nil, nil); err != nil {
		return err
	}
	if err := step("the node lets the customer go", func() (bool, error) {
		on, err := onNode()
		return !on && err == nil, err
	}); err != nil {
		return err
	}
	fmt.Printf("panel %s and node %s work together\n", panelVersion, nodeVersion)
	return nil
}

// versionOf is what a panel says it is.
func versionOf(ctx context.Context, c *upgradecheck.Client) string {
	var out struct {
		Version string `json:"version"`
	}
	if err := c.Do(ctx, http.MethodGet, "api/system", nil, &out); err != nil || out.Version == "" {
		return "unknown"
	}
	return out.Version
}
