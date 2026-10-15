package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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
	base   string
}

func newXUIAPI(node model.Node) (*xuiAPI, error) {
	c, err := clientFor(node, xuiTimeout, nil)
	if err != nil {
		return nil, err
	}
	return &xuiAPI{node: node, client: c, base: strings.TrimRight(node.Address, "/") + "/panel/api/"}, nil
}

type xuiEnvelope struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

func (x *xuiAPI) do(ctx context.Context, method, path string, body, into any) error {
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
	req.Header.Set("Authorization", "Bearer "+x.node.Token)
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
		return errors.New("nothing answered there; the address needs that panel's secret path, as in its access URL")
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
	Email      string `json:"email"`
	LimitIP    int    `json:"limitIp"`
	TotalGB    int64  `json:"totalGB"`
	ExpiryTime int64  `json:"expiryTime"`
	Enable     bool   `json:"enable"`
	SubID      string `json:"subId"`
	Comment    string `json:"comment"`
}

func (x *xuiAPI) addClient(ctx context.Context, inboundID int, c xuiClient) error {
	body := map[string]any{"client": c, "inboundIds": []int{inboundID}}
	return x.do(ctx, http.MethodPost, "clients/add", body, nil)
}

func (x *xuiAPI) updateClient(ctx context.Context, c xuiClient) error {
	return x.do(ctx, http.MethodPost, "clients/update/"+url.PathEscape(c.Email), c, nil)
}

func (x *xuiAPI) deleteClient(ctx context.Context, email string) error {
	return x.do(ctx, http.MethodPost, "clients/del/"+url.PathEscape(email), nil, nil)
}

func (x *xuiAPI) links(ctx context.Context, email string) ([]string, error) {
	var out []string
	err := x.do(ctx, http.MethodGet, "clients/links/"+url.PathEscape(email), nil, &out)
	return out, err
}
