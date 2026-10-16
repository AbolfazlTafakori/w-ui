package nodes

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/service"
)

// Users a 3x-ui or Pasarguard node had before it was one, brought in as this
// panel's customers (service/xuiimport.go): read there as candidates, and
// then, round by round, switched on and off where they are and counted --
// never made again, moved or removed, so what their owner gave them keeps
// working.

// adoptedMark is what a round learned of a brought-in file: that this panel
// left it on or off there, and whether it was found switched off there by
// hand, which this panel then keeps.
type adoptedMark struct {
	on       bool
	offThere bool
}

// saveMarks keeps what the round left each brought-in file at.
func (s *Syncer) saveMarks(ctx context.Context, marks map[uint]adoptedMark) error {
	db := s.db.WithContext(ctx)
	for id, m := range marks {
		up := map[string]any{"xui_on": m.on}
		if m.offThere {
			up["enabled"] = false
		}
		if err := db.Model(&model.Account{}).Where("id = ?", id).Updates(up).Error; err != nil {
			return fmt.Errorf("keeping what a brought-in user was left at: %w", err)
		}
	}
	return nil
}

// madeByAPanel reports whether a name there is one a W-UI panel made --
// this one or another sharing the node -- and so no user to bring in.
func madeByAPanel(name string) bool {
	for _, p := range []string{"wui-", "wui_"} {
		rest, ok := strings.CutPrefix(name, p)
		if !ok {
			continue
		}
		// wui-<n>, as before panels had a tag, or wui-<tag>-<n>.
		if _, n, ok := strings.Cut(rest, p[3:]); ok {
			rest = n
		}
		if rest != "" && strings.Trim(rest, "0123456789") == "" {
			return true
		}
	}
	return strings.HasPrefix(name, "wuikeep_")
}

// xuiAdopted is one round for a brought-in 3x-ui client: switched as this
// panel wants it, its own limits cleared, its usage read, its links when due.
func (s *Syncer) xuiAdopted(ctx context.Context, api *xuiAPI, acc *model.Account, rc *remoteXUIClient, on, refresh bool, marks map[uint]adoptedMark) (bool, *service.XUIUsage, string, error) {
	if rc == nil {
		return false, nil, "", fmt.Errorf("%s, brought in from that panel, is no longer there", acc.Adopted)
	}
	mark := adoptedMark{on: on}
	if on && !rc.cfg.Enable && acc.XUIOn {
		// Left on, found off: switched off there by its owner.
		on, mark = false, adoptedMark{on: false, offThere: true}
	}
	changed := false
	if rc.cfg.Enable != on || rc.cfg.TotalGB != 0 || rc.cfg.ExpiryTime != 0 {
		c := map[string]any{}
		for k, v := range rc.raw {
			c[k] = v
		}
		c["email"], c["enable"], c["totalGB"], c["expiryTime"] = acc.Adopted, on, 0, 0
		if err := api.updateRawClient(ctx, rc.inbound, xuiKey(rc.kind, rc.cfg), acc.Adopted, c); err != nil {
			return false, nil, "", fmt.Errorf("switching %s: %v", acc.Adopted, err)
		}
		changed = true
	}
	if mark.on != acc.XUIOn || mark.offThere {
		marks[acc.ID] = mark
	}
	var u *service.XUIUsage
	if rc.hasStat {
		x := service.XUIUsage{AccountID: acc.ID, ClientID: acc.ClientID, SeenUp: rc.stat.Up, SeenDown: rc.stat.Down,
			Up: since(rc.stat.Up, acc.XUIUp), Down: since(rc.stat.Down, acc.XUIDown)}
		if x.Up > 0 || x.Down > 0 || x.SeenUp != acc.XUIUp || x.SeenDown != acc.XUIDown {
			u = &x
		}
	}
	link := ""
	if refresh || acc.XUILink == "" {
		got, err := api.adoptedLinks(ctx, acc.Adopted, rc.cfg.SubID, xuiCredential(rc.kind, rc.cfg))
		if err != nil {
			return changed, u, "", fmt.Errorf("reading the links of %s: %v", acc.Adopted, err)
		}
		if joined := strings.Join(got, "\n"); joined != acc.XUILink && joined != "" {
			link = joined
		}
	}
	return changed, u, link, nil
}

// xuiCredential is what of a client its links carry, by its protocol.
func xuiCredential(kind string, c xuiInboundClient) string {
	switch kind {
	case "vless", "vmess":
		return c.ID
	case "hysteria", "hysteria2":
		return c.Auth
	case "mtproto":
		return c.Secret
	case "wireguard", "amneziawg":
		return c.PrivateKey
	case "tuic":
		return c.ID
	}
	return c.Password
}

// xuiCandidates are a 3x-ui node's own clients as customers to bring in: the
// clients that share a subscription id are one customer, as that panel's
// subscription makes them one, with a file on each inbound they are on.
func xuiCandidates(ctx context.Context, node model.Node, tunnels []model.Interface) ([]service.ImportCandidate, error) {
	api, err := newXUIAPI(ctx, node)
	if err != nil {
		return nil, err
	}
	rows, err := api.inbounds(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading its inbounds: %w", err)
	}
	tunnelOf := map[int]model.Interface{}
	for _, t := range tunnels {
		if !t.XUI.V.Gone {
			tunnelOf[t.XUI.V.RemoteID] = t
		}
	}
	var out []service.ImportCandidate
	index := map[string]int{}
	seenEmail := map[string]int{} // which candidate an email went to
	for _, r := range rows {
		var set xuiSettings
		_ = objectOrString(r.Settings, &set)
		stats := map[string]xuiClientStat{}
		for _, cs := range r.ClientStats {
			stats[cs.Email] = cs
		}
		t, ok := tunnelOf[r.ID]
		for _, c := range set.Clients {
			// One client in two inbounds (3x-ui 3.1 and later) is one user,
			// counted once.
			if c.Email == "" || madeByAPanel(c.Email) {
				continue
			}
			if i, had := seenEmail[c.Email]; had {
				out[i].Inbounds = append(out[i].Inbounds, r.Remark)
				continue
			}
			key := c.SubID
			if key == "" {
				key = c.Email
			}
			// Two of its clients on one inbound are two users.
			if i, had := index[key]; had && candidateOn(out[i], t.ID) {
				key = c.Email
			}
			i, had := index[key]
			if !had {
				name := strings.TrimSpace(c.Comment)
				if name == "" {
					name = c.Email
				}
				out = append(out, service.ImportCandidate{Key: key, Name: name})
				i = len(out) - 1
				index[key] = i
			}
			seenEmail[c.Email] = i
			cand := &out[i]
			st := stats[c.Email]
			cand.UsedBytes += st.Up + st.Down
			cand.Enabled = cand.Enabled || c.Enable
			if c.TotalGB > 0 && uint64(c.TotalGB) > cand.QuotaBytes {
				cand.QuotaBytes = uint64(c.TotalGB)
			}
			switch {
			case c.ExpiryTime > 0:
				at := time.UnixMilli(c.ExpiryTime).UTC()
				if cand.ExpiresAt == nil || at.After(*cand.ExpiresAt) {
					cand.ExpiresAt = &at
				}
			case c.ExpiryTime < 0:
				// That many milliseconds from the first connection.
				if d := int(math.Ceil(float64(-c.ExpiryTime) / float64(24*time.Hour/time.Millisecond))); d > cand.DurationDays {
					cand.DurationDays = d
				}
			}
			if c.LimitIP > cand.DeviceLimit {
				cand.DeviceLimit = c.LimitIP
			}
			cand.Inbounds = append(cand.Inbounds, r.Remark)
			if !ok || !t.XUI.V.Customers {
				if cand.Reason == "" {
					cand.Reason = fmt.Sprintf("the inbound %q is not one customers can be put on here", r.Remark)
				}
				continue
			}
			cand.Files = append(cand.Files, service.ImportFile{InterfaceID: t.ID, AdoptedAccount: service.AdoptedAccount{
				Remote: c.Email, Secret: xuiAdoptedSecret(r.Protocol, c), Up: st.Up, Down: st.Down, On: c.Enable,
			}})
		}
	}
	for i := range out {
		if len(out[i].Files) > 0 {
			out[i].Reason = "" // on at least one inbound that takes customers
		} else if out[i].Reason == "" {
			out[i].Reason = "on no inbound customers can be put on here"
		}
	}
	return out, nil
}

func candidateOn(c service.ImportCandidate, ifaceID uint) bool {
	for _, f := range c.Files {
		if f.InterfaceID == ifaceID {
			return true
		}
	}
	return false
}

// xuiAdoptedSecret is a brought-in client's credential as this panel keeps
// a file's (xuiClientFor reads it the same way).
func xuiAdoptedSecret(kind string, c xuiInboundClient) string {
	switch kind {
	case "tuic":
		return c.ID + "|" + c.Password
	case "mtproto":
		return c.Secret
	}
	return xuiCredential(kind, c)
}

// pgCandidates are a Pasarguard node's own users as customers to bring in.
// A user there reaches every inbound of its groups and has one usage, so it
// is one file here, on the first of those inbounds held here; its links, from
// its own subscription, carry all of them.
func pgCandidates(ctx context.Context, node model.Node, tunnels []model.Interface) ([]service.ImportCandidate, error) {
	api, err := newPGAPI(ctx, node)
	if err != nil {
		return nil, err
	}
	users, err := api.users(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("reading its users: %w", err)
	}
	gs, err := api.groups(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading its groups: %w", err)
	}
	tagsOf := map[int][]string{}
	for _, g := range gs {
		tagsOf[g.ID] = g.InboundTags
	}
	tunnelOf := map[string]model.Interface{}
	for _, t := range tunnels {
		if !t.XUI.V.Gone {
			tunnelOf[t.XUI.V.Tag] = t
		}
	}
	var out []service.ImportCandidate
	for _, u := range users {
		if madeByAPanel(u.Username) {
			continue
		}
		c := service.ImportCandidate{
			Key: u.Username, Name: u.Username, Note: u.Note,
			Enabled:   u.Status != "disabled",
			UsedBytes: u.UsedTraffic,
		}
		if u.DataLimit != nil {
			c.QuotaBytes = *u.DataLimit
		}
		if at := pgTime(u.Expire); at != nil {
			c.ExpiresAt = at
		} else if u.Status == "on_hold" && u.OnHoldExpireDuration > 0 {
			c.DurationDays = int(math.Ceil(float64(u.OnHoldExpireDuration) / 86400))
		}
		var file *service.ImportFile
		for _, gid := range u.GroupIDs {
			for _, tag := range tagsOf[gid] {
				c.Inbounds = append(c.Inbounds, tag)
				if t, ok := tunnelOf[tag]; ok && t.XUI.V.Customers && file == nil {
					file = &service.ImportFile{InterfaceID: t.ID, AdoptedAccount: service.AdoptedAccount{
						Remote: u.Username, Secret: pgSecret(u), Up: u.UsedTraffic,
						On: u.Status == "active" || u.Status == "on_hold",
					}}
				}
			}
		}
		if file != nil {
			c.Files = []service.ImportFile{*file}
		} else {
			c.Reason = "in no group with an inbound customers can be put on here"
		}
		out = append(out, c)
	}
	return out, nil
}

// pgSecret is a Pasarguard user's credential, the first its proxies have.
func pgSecret(u pgUser) string {
	for _, k := range []string{"vless", "vmess", "trojan", "shadowsocks", "hysteria"} {
		if m, ok := u.ProxySettings[k].(map[string]any); ok {
			for _, f := range []string{"id", "password", "auth"} {
				if s, ok := m[f].(string); ok && s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// pgTime reads an expiry Pasarguard gives as a date-time or a Unix time.
func pgTime(v any) *time.Time {
	switch x := v.(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05"} {
			if t, err := time.Parse(layout, x); err == nil {
				t = t.UTC()
				return &t
			}
		}
	case float64:
		if x > 0 {
			t := time.Unix(int64(x), 0).UTC()
			return &t
		}
	}
	return nil
}

// pgAdopted is one round for a brought-in Pasarguard user: switched as this
// panel wants it, its own limits cleared, its usage read, its links when due.
// Its groups are its owner's and left as they are.
func (s *Syncer) pgAdopted(ctx context.Context, api *pgAPI, acc *model.Account, u *pgUser, on, refresh bool, marks map[uint]adoptedMark) (*service.XUIUsage, string, error) {
	if u == nil {
		return nil, "", fmt.Errorf("%s, brought in from that panel, is no longer there", acc.Adopted)
	}
	mark := adoptedMark{on: on}
	if on && u.Status == "disabled" && acc.XUIOn {
		on, mark = false, adoptedMark{on: false, offThere: true}
	}
	status := "disabled"
	if on {
		status = "active"
	}
	limited := u.DataLimit != nil && *u.DataLimit > 0 || pgTime(u.Expire) != nil
	if u.Status != status || limited {
		if err := api.releaseUser(ctx, acc.Adopted, status); err != nil {
			return nil, "", fmt.Errorf("switching %s: %v", acc.Adopted, err)
		}
	}
	if mark.on != acc.XUIOn || mark.offThere {
		marks[acc.ID] = mark
	}
	use := &service.XUIUsage{AccountID: acc.ID, ClientID: acc.ClientID, Down: since(u.UsedTraffic, acc.XUIUp), SeenUp: u.UsedTraffic}
	link := ""
	if on && (refresh || acc.XUILink == "") {
		sub := u.SubscriptionURL
		if sub == "" {
			if full, err := api.user(ctx, acc.Adopted); err == nil {
				sub = full.SubscriptionURL
			}
		}
		got, err := api.links(ctx, sub)
		if err != nil {
			return use, "", fmt.Errorf("reading the links of %s: %v", acc.Adopted, err)
		}
		if joined := strings.Join(got, "\n"); joined != acc.XUILink && joined != "" {
			link = joined
		}
	}
	return use, link, nil
}

// ImportCandidates reads a 3x-ui or Pasarguard node's own users as customers
// to bring in (service.ImportCandidates).
func ImportCandidates(ctx context.Context, node model.Node, tunnels []model.Interface) ([]service.ImportCandidate, error) {
	switch node.Kind {
	case model.KindXUI:
		return xuiCandidates(ctx, node, tunnels)
	case model.KindPG:
		return pgCandidates(ctx, node, tunnels)
	}
	return nil, fmt.Errorf("users are brought in only from a 3x-ui or Pasarguard node")
}
