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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// Speaking to a 3x-ui panel added as a node.
//
// Its API has changed three times, and each generation is spoken as it is:
//
//   - before 2.8, signed in by username and password: clients are added and
//     changed inside their inbound's settings, removed by their credential,
//     and the server's status and xray restart live under /server;
//   - 2.8 to 3.0, the same with the server under /panel/api and clients
//     removed by email, and from 3.0 an API token as a bearer;
//   - from 3.1, with an API token: clients have an API of their own, by
//     email, which also gives each one's links.
//
// The xray config is under /panel/xray, signed in, until 3.3, and under the
// API from then. Links come from the API from 3.1, and before it from the
// panel's own subscription service. Every answer is the same envelope:
// success, a message, and the object.

const xuiTimeout = 20 * time.Second

type xuiGen int

const (
	xuiGenOld xuiGen = iota // before 2.8
	xuiGenMid               // 2.8 to 3.0
	xuiGenNew               // 3.1 and later
)

type xuiAPI struct {
	node   model.Node
	client *http.Client
	root   string // the panel's address, with its secret path
	base   string // its API, under root
	// token and session are the two ways in: a bearer token, and a sign-in
	// whose cookie is kept. A panel may be given both, which 3.0 to 3.2 need
	// for the routes still outside the API.
	token   bool
	session bool
	gen     xuiGen
	version string
}

// xuiInfo is what was learned of a panel: its version and API generation.
type xuiInfo struct {
	version string
	gen     xuiGen
}

var (
	// xuiSessions holds each signed-in panel's cookies, by node and the
	// sign-in it was made with: a 3x-ui panel tells its operator of every
	// sign-in, on Telegram among other places, and one every twenty seconds
	// would bury them.
	xuiSessions sync.Map
	// xuiInfos is each panel's version, learned once and again when the
	// panel is restarted or upgraded (see newXUIAPI).
	xuiInfos sync.Map
)

func xuiSessionKey(node model.Node) string {
	return fmt.Sprintf("%d|%s|%s|%s", node.ID, node.Address, node.Username, node.Password)
}

func xuiInfoKey(node model.Node) string {
	return fmt.Sprintf("%d|%s", node.ID, node.Address)
}

func newXUIAPI(ctx context.Context, node model.Node) (*xuiAPI, error) {
	c, err := clientFor(node, xuiTimeout, nil)
	if err != nil {
		return nil, err
	}
	root := strings.TrimRight(node.Address, "/") + "/"
	x := &xuiAPI{node: node, client: c, root: root, base: root + "panel/api/",
		token: node.Token != "", session: node.Username != "" && node.Password != ""}
	if !x.token && !x.session {
		return nil, errors.New("give it an API token, or a username and password")
	}
	if x.session {
		if jar, ok := xuiSessions.Load(xuiSessionKey(node)); ok {
			c.Jar = jar.(http.CookieJar)
		} else if err := x.signIn(ctx); err != nil {
			return nil, err
		}
	}
	if info, ok := xuiInfos.Load(xuiInfoKey(node)); ok {
		x.version, x.gen = info.(xuiInfo).version, info.(xuiInfo).gen
	} else if err := x.learn(ctx); err != nil {
		return nil, err
	}
	switch {
	case x.gen == xuiGenOld && !x.session:
		return nil, errors.New("3x-ui before 2.8 has no API token: give it the panel's username and password")
	case x.gen == xuiGenNew && !x.token:
		return nil, errors.New("3x-ui 3.1 and later is reached by API token: make one under Settings, API tokens")
	}
	return x, nil
}

// pageVersion reads the version a 3x-ui page carries on its assets:
// custom.min.css?2.9.4.
var pageVersion = regexp.MustCompile(`\.(?:css|js)\?v?(\d+\.\d+\.\d+)`)

// learn finds the panel's version, and from it the API generation; with no
// version to go on, the generation is found by asking.
func (x *xuiAPI) learn(ctx context.Context) error {
	if x.token {
		var st xuiStatus
		if err := x.call(ctx, http.MethodGet, x.base+"server/status", nil, &st); err == nil {
			x.version = st.PanelVersion
		}
	}
	if x.version == "" && x.session {
		if page, err := x.fetch(ctx, x.root+"panel/"); err == nil {
			if m := pageVersion.FindStringSubmatch(page); m != nil {
				x.version = m[1]
			}
		}
	}
	switch {
	case x.version != "" && !versionBelow(x.version, 3, 1):
		x.gen = xuiGenNew
	case x.version != "" && !versionBelow(x.version, 2, 8):
		x.gen = xuiGenMid
	case x.version != "":
		x.gen = xuiGenOld
	default:
		var probe json.RawMessage
		switch {
		case x.call(ctx, http.MethodGet, x.base+"clients/list", nil, &probe) == nil:
			x.gen = xuiGenNew
		case x.call(ctx, http.MethodGet, x.base+"server/status", nil, &probe) == nil:
			x.gen = xuiGenMid
		default:
			x.gen = xuiGenOld
		}
	}
	xuiInfos.Store(xuiInfoKey(x.node), xuiInfo{version: x.version, gen: x.gen})
	return nil
}

// versionBelow reports whether v is older than major.minor.
func versionBelow(v string, major, minor int) bool {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) < 2 {
		return false
	}
	ma, err1 := strconv.Atoi(parts[0])
	mi, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return ma < major || ma == major && mi < minor
}

// signIn signs in with the node's username and password and keeps the cookie.
func (x *xuiAPI) signIn(ctx context.Context) error {
	jar, _ := cookiejar.New(nil)
	x.client.Jar = jar
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
		return errNotThere
	}
	var env xuiEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env); err != nil {
		return errors.New("it answered, but not like a 3x-ui panel")
	}
	if !env.Success {
		return errors.New("the username or password was refused (a panel with two-factor sign-in needs an API token instead)")
	}
	xuiSessions.Store(xuiSessionKey(x.node), http.CookieJar(jar))
	return nil
}

type xuiEnvelope struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

var errNotThere = errors.New("nothing answered there; the address needs that panel's secret path, as in its access URL")

// do calls the API, under /panel/api.
func (x *xuiAPI) do(ctx context.Context, method, path string, body, into any) error {
	return x.call(ctx, method, x.base+path, body, into)
}

// call sends a request to the panel and reads its envelope. A kept sign-in
// that has lapsed answers 404, as everything outside it does: it signs in
// again, once, and tries again.
func (x *xuiAPI) call(ctx context.Context, method, target string, body, into any) error {
	err := x.once(ctx, method, target, body, into)
	if x.session && errors.Is(err, errNotThere) {
		xuiSessions.Delete(xuiSessionKey(x.node))
		if lerr := x.signIn(ctx); lerr != nil {
			return lerr
		}
		err = x.once(ctx, method, target, body, into)
	}
	return err
}

func (x *xuiAPI) once(ctx context.Context, method, target string, body, into any) error {
	var rd io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case url.Values:
		rd, contentType = strings.NewReader(b.Encode()), "application/x-www-form-urlencoded"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			return fmt.Errorf("encode: %w", err)
		}
		rd, contentType = bytes.NewReader(raw), "application/json"
	}
	ctx, cancel := context.WithTimeout(ctx, xuiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return fmt.Errorf("bad address: %w", err)
	}
	if x.token {
		req.Header.Set("Authorization", "Bearer "+x.node.Token)
	}
	req.Header.Set("Accept", "application/json")
	// What a browser sends; the older panels answer an unsigned request to
	// their API with a page rather than 404 without it.
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := x.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach it: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		if x.token {
			return errors.New("the API token was refused; make a new one on that 3x-ui panel under Settings, API tokens")
		}
		return errNotThere
	case resp.StatusCode == http.StatusNotFound:
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

// xuiStatus is the part of the server's status this panel shows.
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
	var err error
	if x.gen == xuiGenOld {
		err = x.call(ctx, http.MethodPost, x.root+"server/status", nil, &st)
	} else {
		err = x.do(ctx, http.MethodGet, "server/status", nil, &st)
	}
	if st.PanelVersion == "" {
		st.PanelVersion = x.version
	}
	return st, err
}

func (x *xuiAPI) restartXray(ctx context.Context) error {
	if x.gen == xuiGenOld {
		return x.call(ctx, http.MethodPost, x.root+"server/restartXrayService", nil, nil)
	}
	return x.do(ctx, http.MethodPost, "server/restartXrayService", nil, nil)
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
	Email      string `json:"email"`
	ID         string `json:"id"`
	Password   string `json:"password"`
	Auth       string `json:"auth"`
	Secret     string `json:"secret"`
	PrivateKey string `json:"privateKey"`
	Flow       string `json:"flow"`
	Method     string `json:"method"`
	Enable     bool   `json:"enable"`
	LimitIP    int    `json:"limitIp"`
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
	ID       string `json:"id,omitempty"`
	Password string `json:"password,omitempty"`
	Auth     string `json:"auth,omitempty"`
	// Secret is an MTProto client's; PrivateKey and PublicKey a WireGuard or
	// AmneziaWG one's, the address being given it by that panel.
	Secret     string `json:"secret,omitempty"`
	PrivateKey string `json:"privateKey,omitempty"`
	PublicKey  string `json:"publicKey,omitempty"`
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
	if x.gen != xuiGenNew {
		return x.do(ctx, http.MethodPost, "inbounds/addClient", inClients(inboundID, c), nil)
	}
	body := map[string]any{"client": c, "inboundIds": []int{inboundID}}
	return x.do(ctx, http.MethodPost, "clients/add", body, nil)
}

// updateClient changes a client; the older API finds it by the credential
// it has now (key), as its inbound's protocol names it.
func (x *xuiAPI) updateClient(ctx context.Context, inboundID int, key string, c xuiClient) error {
	if x.gen != xuiGenNew {
		return x.do(ctx, http.MethodPost, "inbounds/updateClient/"+url.PathEscape(key), inClients(inboundID, c), nil)
	}
	return x.do(ctx, http.MethodPost, "clients/update/"+url.PathEscape(c.Email), c, nil)
}

// deleteClient removes a client: by its credential (key) before 2.8, and by
// its email after.
func (x *xuiAPI) deleteClient(ctx context.Context, inboundID int, email, key string) error {
	switch x.gen {
	case xuiGenOld:
		return x.do(ctx, http.MethodPost, fmt.Sprintf("inbounds/%d/delClient/%s", inboundID, url.PathEscape(key)), nil, nil)
	case xuiGenMid:
		return x.do(ctx, http.MethodPost, fmt.Sprintf("inbounds/%d/delClientByEmail/%s", inboundID, url.PathEscape(email)), nil, nil)
	}
	return x.do(ctx, http.MethodPost, "clients/del/"+url.PathEscape(email), nil, nil)
}

// links are the links the panel makes for a client: from its API from 3.1,
// and before it from its own subscription service, by the client's
// subscription id, which this panel sets to the email.
func (x *xuiAPI) links(ctx context.Context, email string) ([]string, error) {
	if x.gen == xuiGenNew {
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

// subURL is where the panel's subscription service answers, read from its
// settings, which are reached signed in.
func (x *xuiAPI) subURL(ctx context.Context) (string, error) {
	if !x.session {
		return "", errors.New("before 3.1 a client's links come from the panel's settings, read signed in: give it the panel's username and password too")
	}
	var set struct {
		Enable   bool   `json:"subEnable"`
		Port     int    `json:"subPort"`
		Path     string `json:"subPath"`
		Domain   string `json:"subDomain"`
		URI      string `json:"subURI"`
		CertFile string `json:"subCertFile"`
	}
	if err := x.call(ctx, http.MethodPost, x.root+"panel/setting/all", nil, &set); err != nil {
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

// fetch reads a page that is not the API: a subscription, the panel's page.
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
