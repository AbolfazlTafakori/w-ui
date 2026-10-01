package upgradecheck

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Verify checks the panel c is signed in to against a manifest written by an
// earlier one, and lists every way they differ. dir is where the manifest's
// files are.
//
// Compared: every interface and customer by id, field by field; each
// customer's subscription token, unchanged; the link the panel gives out now
// answering, with configurations that connect the same way the old ones did;
// and, unless opts says otherwise, the old link exactly as it was handed out,
// still answering.
func Verify(ctx context.Context, c *Client, m *Manifest, dir string, opts Options) []string {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	now := &Manifest{}
	if err := Describe(ctx, c, now); err != nil {
		return []string{err.Error()}
	}

	ifaces := map[uint]Interface{}
	for _, i := range now.Interfaces {
		ifaces[i.ID] = i
	}
	for _, was := range m.Interfaces {
		is, ok := ifaces[was.ID]
		if !ok {
			add("interface %d (%s) is gone", was.ID, was.Name)
			continue
		}
		if was != is {
			add("interface %s changed: was %+v, is %+v", was.Name, was, is)
		}
	}

	// Every setting the earlier panel held, as this one reads it.
	if len(m.Pages) > 0 {
		now, err := ReadPages(ctx, c)
		if err != nil {
			add("reading the settings pages: %v", err)
		} else {
			was := m.Pages
			if opts.NewAddress {
				was = withoutAccess(m.Pages)
			}
			for _, d := range pagesDiff(was, now) {
				add("setting %s", d)
			}
		}
	}

	clients := map[uint]Customer{}
	for _, cu := range now.Clients {
		clients[cu.ID] = cu
	}
	for _, was := range m.Clients {
		is, ok := clients[was.ID]
		if !ok {
			add("customer %d (%s) is gone", was.ID, was.Name)
			continue
		}
		for _, d := range customerDiff(was, is, opts.InUse[was.Name]) {
			add("customer %s: %s", was.Name, d)
		}
		problems = append(problems, verifySubscription(ctx, c, was, dir, opts)...)
	}
	return problems
}

// Options adjusts Verify for how the panel came to hold the data.
type Options struct {
	// NewAddress is a panel the data was restored into rather than updated
	// in place. A restore keeps the receiving panel's own address and
	// subscription port -- it is that server's -- so links name it, not the
	// old one, and only the token is carried over.
	NewAddress bool
	// InUse are customers, by name, whose tunnel carried traffic since the
	// fixture was taken: what they used may have grown, never shrunk.
	InUse map[string]bool
}

// customerDiff lists what changed about one customer.
func customerDiff(was, is Customer, inUse bool) []string {
	var d []string
	field := func(name string, a, b any) {
		if fmt.Sprint(a) != fmt.Sprint(b) {
			d = append(d, fmt.Sprintf("%s was %v, is %v", name, a, b))
		}
	}
	field("name", was.Name, is.Name)
	field("note", was.Note, is.Note)
	field("protocol", was.Protocol, is.Protocol)
	field("status", was.Status, is.Status)
	field("groups", sorted(was.Groups), sorted(is.Groups))
	field("quota", was.QuotaBytes, is.QuotaBytes)
	if !inUse || is.UsedBytes < was.UsedBytes {
		field("used", was.UsedBytes, is.UsedBytes)
	}
	field("starts on first use", was.StartOnFirstUse, is.StartOnFirstUse)
	field("duration (days)", was.DurationDays, is.DurationDays)
	field("device limit", was.DeviceLimit, is.DeviceLimit)
	field("reset cycle", was.ResetCycle, is.ResetCycle)
	field("subscription id", was.SubID, is.SubID)
	switch {
	case (was.ExpiresAt == nil) != (is.ExpiresAt == nil):
		d = append(d, fmt.Sprintf("end date was %v, is %v", was.ExpiresAt, is.ExpiresAt))
	case was.ExpiresAt != nil && !was.ExpiresAt.Equal(*is.ExpiresAt):
		d = append(d, fmt.Sprintf("end date was %s, is %s", was.ExpiresAt, is.ExpiresAt))
	}
	accounts := map[uint]Account{}
	for _, a := range is.Accounts {
		accounts[a.ID] = a
	}
	for _, a := range was.Accounts {
		b, ok := accounts[a.ID]
		switch {
		case !ok:
			d = append(d, fmt.Sprintf("device %d (%s) is gone", a.ID, a.DeviceName))
		case a != b:
			d = append(d, fmt.Sprintf("device %s was %+v, is %+v", a.DeviceName, a, b))
		}
	}
	if len(is.Accounts) != len(was.Accounts) {
		d = append(d, fmt.Sprintf("had %d devices, has %d", len(was.Accounts), len(is.Accounts)))
	}
	return d
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// verifySubscription checks one customer's link, old and new.
func verifySubscription(ctx context.Context, c *Client, was Customer, dir string, opts Options) []string {
	var problems []string
	add := func(format string, a ...any) {
		problems = append(problems, fmt.Sprintf("customer %s: ", was.Name)+fmt.Sprintf(format, a...))
	}
	var link struct {
		Link  string `json:"link"`
		Token string `json:"token"`
	}
	if err := c.Do(ctx, http.MethodGet, fmt.Sprintf("api/clients/%d/subscription", was.ID), nil, &link); err != nil {
		add("subscription link: %v", err)
		return problems
	}
	if link.Token != was.SubToken {
		add("subscription token changed: links already handed out stop working")
	}

	var old []Profile
	if was.SubFile != "" {
		raw, err := os.ReadFile(filepath.Join(dir, was.SubFile))
		if err != nil {
			add("%v", err)
			return problems
		}
		if old, err = ParseProfiles(raw); err != nil {
			add("the fixture's own configuration does not read: %v", err)
		}
	}

	check := func(which, url string) {
		got, err := FetchSubscription(ctx, url)
		if err != nil {
			add("%s link %s: %v", which, url, err)
			return
		}
		if got.Status != was.SubStatus {
			add("%s link answers %d, answered %d", which, got.Status, was.SubStatus)
			return
		}
		if got.Status != http.StatusOK {
			return
		}
		if got.Header.Get("Subscription-Userinfo") == "" {
			add("%s link: no Subscription-Userinfo, which client apps read", which)
		}
		now, err := ParseProfiles(got.Body)
		if err != nil {
			add("%s link: %v", which, err)
			return
		}
		if len(now) == 0 {
			add("%s link serves no configuration", which)
		}
		for _, p := range now {
			if err := p.Validate(); err != nil {
				add("%s link: %v", which, err)
			}
		}
		if err := SameProfiles(old, now); err != nil {
			add("%s link connects differently: %v", which, err)
		}
	}
	check("new", link.Link)
	// The link as the old panel handed it out, character for character.
	if !opts.NewAddress && was.SubLink != "" && !strings.EqualFold(was.SubLink, link.Link) {
		check("old", was.SubLink)
	}
	return problems
}
