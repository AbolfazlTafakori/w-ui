package nodes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Speaking to a 3x-ui panel added as a node.
//
// Its API answers under its own secret path, /panel/api, with an API token
// made on that panel (Settings → API tokens) as a bearer token, and every
// answer is the same envelope: success, a message, and the object. Only
// what this panel needs is read: the inbounds with their clients and
// counters, a client added, changed or removed by its email, and the links
// that panel makes for a client.

// xuiTimeout bounds one call to a 3x-ui panel.
const xuiTimeout = 20 * time.Second

type xuiAPI struct {
	node   model.Node
	client *http.Client
	root   string // the panel's address, with its secret path
	base   string // its API, under root
	// session is a panel signed in to by username and password rather than
	// a token: 3x-ui before version 3, whose API is the older one (legacy).
	session bool
	legacy  bool
}

func newXUIAPI(ctx context.Context, node model.Node) (*xuiAPI, error) {
	c, err := clientFor(node, xuiTimeout, nil)
	if err != nil {
		return nil, err
	}
	root := strings.TrimRight(node.Address, "/") + "/"
	x := &xuiAPI{node: node, client: c, root: root, base: root + "panel/api/"}
	if node.Token == "" && node.Username != "" {
		x.session = true
		// The sign-in is kept from round to round: a 3x-ui panel tells its
		// operator of every sign-in, on Telegram among other places, and one
		// every twenty seconds would bury them.
		key := xuiSessionKey(node)
		if jar, ok := xuiSessions.Load(key); ok {
			c.Jar = jar.(http.CookieJar)
			x.legacy = true
			return x, nil
		}
		jar, _ := cookiejar.New(nil)
		c.Jar = jar
		if err := x.login(ctx); err != nil {
			return nil, err
		}
		// Version 3 has its clients API; before it, clients live in their
		// inbounds' settings and are changed through them.
		var probe json.RawMessage
		if err := x.do(ctx, http.MethodGet, "clients/list", nil, &probe); err == nil {
			return nil, errors.New("this is 3x-ui 3 or later: give it an API token (Settings, API tokens) rather than a password")
		}
		x.legacy = true
		xuiSessions.Store(key, http.CookieJar(jar))
	}
	return x, nil
}

// xuiSessions holds each signed-in 3x-ui node's cookies, by node and the
// sign-in it was made with, so a changed password signs in again.
var xuiSessions sync.Map

func xuiSessionKey(node model.Node) string {
	return fmt.Sprintf("%d|%s|%s|%s", node.ID, node.Address, node.Username, node.Password)
}

// login signs in with the node's username and password; the panel's cookie
// is kept in the client's jar for the rest of the round.
func (x *xuiAPI) login(ctx context.Context) error {
	form := url.Values{"username": {x.node.Username}, "password": {x.node.Password}}
	ctx, cancel := context.WithTimeout(ctx, xuiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, x.root+"login", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("bad address: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := x.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach it: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errors.New("nothing answered there; the address needs that panel's secret path, as in its access URL")
	}
	var env xuiEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env); err != nil {
		return errors.New("it answered, but not like a 3x-ui panel")
	}
	if !env.Success {
		return errors.New("the username or password was refused (a panel with two-factor sign-in needs an API token instead)")
	}
	return nil
}

type xuiEnvelope struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

func (x *xuiAPI) do(ctx context.Context, method, path string, body, into any) error {
	err := x.once(ctx, method, path, body, into)
	// A kept sign-in that has lapsed answers 404, as everything outside it
	// does: sign in again, once, and try again.
	if x.session && errors.Is(err, errNotThere) {
		xuiSessions.Delete(xuiSessionKey(x.node))
		jar, _ := cookiejar.New(nil)
		x.client.Jar = jar
		if lerr := x.login(ctx); lerr != nil {
			return lerr
		}
		xuiSessions.Store(xuiSessionKey(x.node), http.CookieJar(jar))
		err = x.once(ctx, method, path, body, into)
	}
	return err
}

var errNotThere = errors.New("nothing answered there; the address needs that panel's secret path, as in its access URL")

func (x *xuiAPI) once(ctx context.Context, method, path string, body, into any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode: %w", err)
		}
		rd = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(ctx, xuiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, x.base+path, rd)
	if err != nil {
		return fmt.Errorf("bad address: %w", err)
	}
	if !x.session {
		req.Header.Set("Authorization", "Bearer "+x.node.Token)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := x.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach it: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errors.New("the API token was refused; make a new one on that 3x-ui panel under Settings, API tokens")
	case resp.StatusCode == http.StatusNotFound:
		// 3x-ui answers 404 to anything outside its secret path.
		return errNotThere
	case resp.StatusCode >= 300:
		return fmt.Errorf("it answered %s", resp.Status)
	}
	var env xuiEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&env); err != nil {
		return errors.New("it answered, but not like a 3x-ui panel")
	}
	if !env.Success {
		msg := strings.TrimSpace(env.Msg)
		if msg == "" {
			msg = "it refused the request"
		}
		return errors.New(msg)
	}
	if into != nil && len(env.Obj) > 0 && string(env.Obj) != "null" {
		if err := json.Unmarshal(env.Obj, into); err != nil {
			return fmt.Errorf("its answer could not be read: %w", err)
		}
	}
	return nil
}

// xuiStatus is the part of /server/status this panel shows.
type xuiStatus struct {
	CPU float64 `json:"cpu"`
	Mem struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"mem"`
	Xray struct {
		State   string `json:"state"`
		Error   string `json:"errorMsg"`
		Version string `json:"version"`
	} `json:"xray"`
	PanelVersion string `json:"panelVersion"`
	Uptime       int64  `json:"uptime"`
}

func (x *xuiAPI) status(ctx context.Context) (xuiStatus, error) {
	var st xuiStatus
	err := x.do(ctx, http.MethodGet, "server/status", nil, &st)
	return st, err
}

// xuiClientStat is a client's counters on one inbound.
type xuiClientStat struct {
	Email      string `json:"email"`
	Enable     bool   `json:"enable"`
	Up         uint64 `json:"up"`
	Down       uint64 `json:"down"`
	LastOnline int64  `json:"lastOnline"`
}

// xuiInboundRow is an inbound as /inbounds/list gives it. Settings and the
// stream settings come as objects from 3x-ui 3 and as JSON in a string from
// older ones; both are read.
type xuiInboundRow struct {
	ID          int             `json:"id"`
	Up          uint64          `json:"up"`
	Down        uint64          `json:"down"`
	Remark      string          `json:"remark"`
	Enable      bool            `json:"enable"`
	Port        int             `json:"port"`
	Protocol    string          `json:"protocol"`
	Tag         string          `json:"tag"`
	ClientStats []xuiClientStat `json:"clientStats"`
	Settings    json.RawMessage `json:"settings"`
	Stream      json.RawMessage `json:"streamSettings"`
}

// xuiInboundClient is a client as it sits in an inbound's settings.
type xuiInboundClient struct {
	Email    string `json:"email"`
	ID       string `json:"id"`
	Password string `json:"password"`
	Auth     string `json:"auth"`
	Flow     string `json:"flow"`
	Method   string `json:"method"`
	Enable   bool   `json:"enable"`
	LimitIP  int    `json:"limitIp"`
}

type xuiSettings struct {
	Clients []xuiInboundClient `json:"clients"`
	Method  string             `json:"method"`
}

type xuiStream struct {
	Network  string `json:"network"`
	Security string `json:"security"`
}

// objectOrString reads a field 3x-ui sends either as an object or as the
// same object written into a string.
func objectOrString(raw json.RawMessage, into any) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return json.Unmarshal([]byte(s), into)
	}
	return json.Unmarshal(raw, into)
}

func (x *xuiAPI) inbounds(ctx context.Context) ([]xuiInboundRow, error) {
	var rows []xuiInboundRow
	err := x.do(ctx, http.MethodGet, "inbounds/list", nil, &rows)
	return rows, err
}

// xuiClient is a client as this panel writes it.
type xuiClient struct {
	ID         string `json:"id,omitempty"`
	Password   string `json:"password,omitempty"`
	Auth       string `json:"auth,omitempty"`
	Flow       string `json:"flow,omitempty"`
	// Method is a Shadowsocks client's cipher. An inbound on one of the
	// older ciphers needs it on every client: without it xray refuses the
	// whole config, and every inbound on that panel stops, its own
	// operator's included.
	Method     string `json:"method,omitempty"`
	Email      string `json:"email"`
	LimitIP    int    `json:"limitIp"`
	TotalGB    int64  `json:"totalGB"`
	ExpiryTime int64  `json:"expiryTime"`
	Enable     bool   `json:"enable"`
	SubID      string `json:"subId"`
	Comment    string `json:"comment"`
}

// inClients is a client as the older API takes it: inside an inbound's
// settings, written as a string.
func inClients(inboundID int, c xuiClient) map[string]any {
	raw, _ := json.Marshal(map[string]any{"clients": []xuiClient{c}})
	return map[string]any{"id": inboundID, "settings": string(raw)}
}

func (x *xuiAPI) addClient(ctx context.Context, inboundID int, c xuiClient) error {
	if x.legacy {
		return x.do(ctx, http.MethodPost, "inbounds/addClient", inClients(inboundID, c), nil)
	}
	body := map[string]any{"client": c, "inboundIds": []int{inboundID}}
	return x.do(ctx, http.MethodPost, "clients/add", body, nil)
}

// updateClient changes a client; the older API finds it by the credential
// it has now (key), as its inbound's protocol names it.
func (x *xuiAPI) updateClient(ctx context.Context, inboundID int, key string, c xuiClient) error {
	if x.legacy {
		return x.do(ctx, http.MethodPost, "inbounds/updateClient/"+url.PathEscape(key), inClients(inboundID, c), nil)
	}
	return x.do(ctx, http.MethodPost, "clients/update/"+url.PathEscape(c.Email), c, nil)
}

func (x *xuiAPI) deleteClient(ctx context.Context, inboundID int, email string) error {
	if x.legacy {
		return x.do(ctx, http.MethodPost, fmt.Sprintf("inbounds/%d/delClientByEmail/%s", inboundID, url.PathEscape(email)), nil, nil)
	}
	return x.do(ctx, http.MethodPost, "clients/del/"+url.PathEscape(email), nil, nil)
}

// links are the links the panel makes for a client: from its API, or before
// version 3 from its own subscription service, by the client's subscription
// id, which this panel sets to the email.
func (x *xuiAPI) links(ctx context.Context, email string) ([]string, error) {
	if !x.legacy {
		var out []string
		err := x.do(ctx, http.MethodGet, "clients/links/"+url.PathEscape(email), nil, &out)
		return out, err
	}
	sub, err := x.subURL(ctx)
	if err != nil {
		return nil, err
	}
	body, err := x.fetch(ctx, sub+url.PathEscape(email))
	if err != nil {
		return nil, fmt.Errorf("its subscription service: %w", err)
	}
	if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body)); err == nil {
		body = string(dec)
	}
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if l = strings.TrimSpace(l); strings.Contains(l, "://") {
			out = append(out, l)
		}
	}
	return out, nil
}

// subURL is where the older panel's subscription service answers, read from
// its settings.
func (x *xuiAPI) subURL(ctx context.Context) (string, error) {
	var set struct {
		Enable   bool   `json:"subEnable"`
		Port     int    `json:"subPort"`
		Path     string `json:"subPath"`
		Domain   string `json:"subDomain"`
		URI      string `json:"subURI"`
		CertFile string `json:"subCertFile"`
	}
	if err := x.post(ctx, x.root+"panel/setting/all", &set); err != nil {
		return "", fmt.Errorf("reading its settings: %w", err)
	}
	if !set.Enable {
		return "", errors.New("its subscription service is off; turn it on under Settings, Subscription, where its links come from")
	}
	if set.URI != "" {
		return strings.TrimRight(set.URI, "/") + "/", nil
	}
	host := set.Domain
	if host == "" {
		u, _ := url.Parse(x.root)
		host = u.Hostname()
	}
	scheme := "http"
	if set.CertFile != "" {
		scheme = "https"
	}
	path := "/" + strings.Trim(set.Path, "/") + "/"
	return fmt.Sprintf("%s://%s%s", scheme, net.JoinHostPort(host, fmt.Sprint(set.Port)), path), nil
}

// post calls a route outside /panel/api that answers the usual envelope.
func (x *xuiAPI) post(ctx context.Context, target string, into any) error {
	ctx, cancel := context.WithTimeout(ctx, xuiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		return err
	}
	resp, err := x.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env xuiEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&env); err != nil || !env.Success {
		return fmt.Errorf("it answered %s", resp.Status)
	}
	return json.Unmarshal(env.Obj, into)
}

// fetch reads a page that is not the API: the subscription.
func (x *xuiAPI) fetch(ctx context.Context, target string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, xuiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "v2rayN")
	resp, err := x.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("it answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return string(b), err
}
