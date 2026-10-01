// Package upgradecheck is what the upgrade tests share: putting a known set of
// interfaces and customers into a panel of any release, writing down what
// that panel then says about them, and checking a later panel -- after a
// migration, a restore or an update -- still says the same.
//
// It talks to the panel over its HTTP API only, the way an operator's browser
// and the customers' apps do, so it runs against a release from before any of
// this existed: the API it uses has kept its shape since v1.0.0.
package upgradecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

// Client is a signed-in administrator's view of one panel.
type Client struct {
	// Base is the panel's address with its URL path, ending in a slash:
	// http://127.0.0.1:2053/abc/.
	Base  string
	http  *http.Client
	token string
}

// Login signs in. The session is held as the panel hands it out -- a bearer
// token and the cookie it is bound to -- so a release that checks either
// accepts it.
func Login(ctx context.Context, base, username, password string) (*Client, error) {
	jar, _ := cookiejar.New(nil)
	c := &Client{
		Base: strings.TrimRight(base, "/") + "/",
		http: &http.Client{Jar: jar, Timeout: 30 * time.Second},
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := c.Do(ctx, http.MethodPost, "api/auth/login",
		map[string]string{"username": username, "password": password}, &out); err != nil {
		return nil, fmt.Errorf("sign in: %w", err)
	}
	if out.Token == "" {
		return nil, fmt.Errorf("sign in: the panel answered without a session")
	}
	c.token = out.Token
	return c, nil
}

// WaitUp waits for the panel to answer on base at all.
func WaitUp(ctx context.Context, base string, within time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, within)
	defer cancel()
	hc := &http.Client{Timeout: 2 * time.Second}
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/", nil)
		if resp, err := hc.Do(req); err == nil {
			resp.Body.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the panel at %s did not answer within %s", base, within)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Do sends body as JSON to path, relative to Base, and decodes the answer
// into out when out is not nil. Anything but a 2xx is an error carrying what
// the panel said.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+strings.TrimLeft(path, "/"), rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: the answer is not what was expected: %w", method, path, err)
	}
	return nil
}

// Raw sends body as JSON to path and returns the status and body as they
// came, whatever the status: for a test that keeps the answer itself.
func (c *Client) Raw(ctx context.Context, method, path string, body any) (int, []byte) {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+strings.TrimLeft(path, "/"), rd)
	if err != nil {
		return 0, []byte(err.Error())
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// Download fetches path, relative to Base, as the signed-in administrator.
func (c *Client) Download(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+strings.TrimLeft(path, "/"), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return raw, nil
}

// Fetched is a subscription link's answer.
type Fetched struct {
	Status int
	Header http.Header
	Body   []byte
}

// FetchSubscription asks a subscription link for its configuration, as a
// client app does: no browser, no session.
func FetchSubscription(ctx context.Context, link string) (*Fetched, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	// What the WireGuard and OpenVPN apps send; a browser would get the page.
	req.Header.Set("User-Agent", "WireGuard/1.0.20231018")
	req.Header.Set("Accept", "*/*")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Fetched{Status: resp.StatusCode, Header: resp.Header, Body: raw}, nil
}
