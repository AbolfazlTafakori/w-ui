package nodes

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// Making an inbound on a 3x-ui or Pasarguard node from this panel's form.
//
// The form asks for what an operator decides -- the protocol, the port, the
// transport, Reality or not -- and the rest is drawn here: Reality's key pair
// and short id, a Shadowsocks server key. The inbound is made on that panel
// as its own, with no clients; this panel's customers are added to it as for
// any inbound read from there.

// CreateXrayInbound makes the inbound on the node and says what it is.
func CreateXrayInbound(ctx context.Context, node model.Node, in service.XrayInboundInput) (model.XUIInbound, error) {
	settings, stream, err := inboundJSON(in)
	if err != nil {
		return model.XUIInbound{}, err
	}
	out := model.XUIInbound{
		Remark: in.Remark, Kind: in.Kind, Network: in.Transport, Security: in.Security,
		Method: in.Method, Enabled: true,
	}
	if in.Kind == "vless" && in.Transport == "tcp" && in.Security == "reality" {
		out.Flow = "xtls-rprx-vision"
	}
	switch node.Kind {
	case model.KindXUI:
		api, err := newXUIAPI(ctx, node)
		if err != nil {
			return out, err
		}
		id, tag, err := api.addInbound(ctx, in.Remark, in.Port, in.Kind, settings, stream)
		if err != nil {
			return out, err
		}
		out.RemoteID, out.Tag = id, tag
		return out, nil
	case model.KindPG:
		api, err := newPGAPI(ctx, node)
		if err != nil {
			return out, err
		}
		if err := api.addInbound(ctx, in, settings, stream, xuiHost(node.Address)); err != nil {
			return out, err
		}
		out.RemoteID, out.Tag = pgRemoteID(in.Remark), in.Remark
		return out, nil
	}
	return out, errors.New("inbounds are made here only on 3x-ui and Pasarguard nodes")
}

// inboundJSON is the inbound's settings and stream settings.
func inboundJSON(in service.XrayInboundInput) (map[string]any, map[string]any, error) {
	settings := map[string]any{"clients": []any{}}
	switch in.Kind {
	case "vless":
		settings["decryption"] = "none"
	case "shadowsocks":
		settings["method"] = in.Method
		settings["network"] = "tcp,udp"
		size := 32
		if in.Method == "2022-blake3-aes-128-gcm" {
			size = 16
		}
		key := make([]byte, size)
		if _, err := rand.Read(key); err != nil {
			return nil, nil, err
		}
		settings["password"] = base64.StdEncoding.EncodeToString(key)
	}
	stream := map[string]any{"network": in.Transport, "security": "none"}
	switch in.Transport {
	case "ws":
		stream["wsSettings"] = map[string]any{"path": in.Path}
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": in.Path}
	default:
		stream["tcpSettings"] = map[string]any{"header": map[string]any{"type": "none"}}
	}
	if in.Security == "reality" {
		priv, pub, err := x25519()
		if err != nil {
			return nil, nil, err
		}
		sid := make([]byte, 4)
		if _, err := rand.Read(sid); err != nil {
			return nil, nil, err
		}
		stream["security"] = "reality"
		stream["realitySettings"] = map[string]any{
			"show": false, "dest": in.SNI + ":443", "target": in.SNI + ":443",
			"serverNames": []string{in.SNI}, "privateKey": priv, "shortIds": []string{hex.EncodeToString(sid)},
			// What 3x-ui writes links from; Pasarguard reads its own.
			"settings": map[string]any{"publicKey": pub, "fingerprint": "chrome", "spiderX": "/"},
		}
	}
	return settings, stream, nil
}

// x25519 is a Reality key pair, as xray writes them.
func x25519() (string, string, error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(k.Bytes()), enc.EncodeToString(k.PublicKey().Bytes()), nil
}

// addInbound makes an inbound on a 3x-ui panel; its id and tag there.
func (x *xuiAPI) addInbound(ctx context.Context, remark string, port int, protocol string, settings, stream map[string]any) (int, string, error) {
	enc := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	body := map[string]any{
		"remark": remark, "enable": true, "port": port, "protocol": protocol,
		"settings": enc(settings), "streamSettings": enc(stream),
		"sniffing": enc(map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}}),
	}
	var made struct {
		ID  int    `json:"id"`
		Tag string `json:"tag"`
	}
	if err := x.do(ctx, http.MethodPost, "inbounds/add", body, &made); err != nil {
		return 0, "", fmt.Errorf("the 3x-ui panel refused the inbound: %w", err)
	}
	return made.ID, made.Tag, nil
}

// addInbound puts an inbound in a Pasarguard panel's first core, restarts its
// nodes with it, and gives it a host so its users have links.
func (p *pgAPI) addInbound(ctx context.Context, in service.XrayInboundInput, settings, stream map[string]any, host string) error {
	var cores struct {
		Cores []struct {
			ID     int            `json:"id"`
			Name   string         `json:"name"`
			Config map[string]any `json:"config"`
		} `json:"cores"`
	}
	if err := p.do(ctx, http.MethodGet, "/api/cores", nil, &cores); err != nil {
		return err
	}
	if len(cores.Cores) == 0 {
		return errors.New("the Pasarguard panel has no core to put the inbound in")
	}
	core := cores.Cores[0]
	list, _ := core.Config["inbounds"].([]any)
	for _, o := range list {
		m, _ := o.(map[string]any)
		if m["tag"] == in.Remark {
			return fmt.Errorf("an inbound called %q is already on that Pasarguard panel", in.Remark)
		}
		if fmt.Sprint(m["port"]) == fmt.Sprint(in.Port) {
			return fmt.Errorf("port %d is already an inbound's on that Pasarguard panel", in.Port)
		}
	}
	list = append(list, map[string]any{
		"tag": in.Remark, "listen": "0.0.0.0", "port": in.Port, "protocol": in.Kind,
		"settings": settings, "streamSettings": stream,
		"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
	})
	core.Config["inbounds"] = list
	name := core.Name
	if name == "" {
		name = "core"
	}
	if err := p.do(ctx, http.MethodPut, fmt.Sprintf("/api/core/%d?restart_nodes=true", core.ID), map[string]any{
		"name": name, "config": core.Config, "exclude_inbound_tags": []string{}, "fallbacks_inbound_tags": []string{},
	}, nil); err != nil {
		return fmt.Errorf("the Pasarguard panel refused the inbound: %w", err)
	}
	h := map[string]any{"remark": "{USERNAME} " + in.Remark, "address": []string{host}, "port": in.Port, "inbound_tag": in.Remark, "priority": 0}
	if in.Security == "reality" {
		h["sni"] = []string{in.SNI}
		h["fingerprint"] = "chrome"
	}
	if err := p.do(ctx, http.MethodPost, "/api/host/", h, nil); err != nil {
		return fmt.Errorf("the inbound is made, but its host could not be: %w", err)
	}
	return nil
}
