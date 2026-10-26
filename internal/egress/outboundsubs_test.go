package egress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func subServer(t *testing.T, body *string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(*body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func tags(t *testing.T, outs *Outbounds) string {
	t.Helper()
	all, err := outs.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ob := range all {
		out = append(out, ob.Tag)
	}
	return strings.Join(out, ",")
}

// An outbound moved to the top -- the default every customer leaves
// through -- stays there when its subscription is fetched again, and the
// subscription's new outbounds never go above it.
func TestARefreshKeepsTheOrderAndTheDefault(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	outs := NewOutbounds(db, nil, quietLog())
	if err := outs.EnsureBuiltins(ctx); err != nil {
		t.Fatal(err)
	}
	subs := NewOutboundSubs(db, outs, quietLog())
	on := true
	body := "socks://1.2.3.4:1080#a\nsocks://1.2.3.5:1080#b\n"
	sub, err := subs.Create(ctx, OutboundSubInput{Remark: "s", URL: subServer(t, &body), TagPrefix: "s-",
		IntervalMin: 10, Enabled: &on, AllowPrivate: true, Prepend: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := tags(t, outs); got != "direct,blocked,s-a,s-b" {
		t.Fatalf("first fetch: %s", got)
	}

	all, _ := outs.List(ctx)
	byTag := map[string]uint{}
	for _, ob := range all {
		byTag[ob.Tag] = ob.ID
	}
	if err := outs.Reorder(ctx, []uint{byTag["s-b"], byTag["direct"], byTag["s-a"], byTag["blocked"]}); err != nil {
		t.Fatal(err)
	}
	if err := subs.Refresh(ctx, sub.ID); err != nil {
		t.Fatal(err)
	}
	if got := tags(t, outs); got != "s-b,direct,s-a,blocked" {
		t.Fatalf("a refresh moved the operator's order: %s", got)
	}

	body += "socks://1.2.3.6:1080#c\n"
	if err := subs.Refresh(ctx, sub.ID); err != nil {
		t.Fatal(err)
	}
	if got := tags(t, outs); got != "s-b,s-c,direct,s-a,blocked" {
		t.Fatalf("a new outbound went above the default or lost the order: %s", got)
	}
}
