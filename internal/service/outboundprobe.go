package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// An HTTP probe sends a real request through the outbound and reads back what
// the far side saw. Cloudflare's trace endpoint answers with the caller's
// address and country in a few plain lines, which is exactly the two things an
// operator wants to know about an exit, and it is what 3x-ui asks too.
const traceURL = "https://www.cloudflare.com/cdn-cgi/trace"

const probeTimeout = 10 * time.Second

type probeResult struct {
	latency time.Duration
	egress  Egress
	err     error
}

// probeThrough fetches the trace through the outbound and times it the way
// the mode asks: Real is connect plus request, HTTP is the request alone once
// a connection exists.
func probeThrough(ctx context.Context, ob *model.Outbound, mode string) probeResult {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	tr, err := transportFor(ob)
	if err != nil {
		return probeResult{err: err}
	}
	defer tr.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, traceURL, nil)
	if err != nil {
		return probeResult{err: err}
	}
	req.Header.Set("User-Agent", "w-ui-probe")

	start := time.Now()
	var gotConn time.Time
	req = req.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { gotConn = time.Now() },
	}))

	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return probeResult{err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return probeResult{err: fmt.Errorf("the trace answered %s", resp.Status)}
	}
	eg, err := parseTrace(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return probeResult{err: err}
	}
	end := time.Now()

	lat := end.Sub(start)
	if mode == ModeHTTP && !gotConn.IsZero() {
		lat = end.Sub(gotConn)
	}
	return probeResult{latency: lat, egress: eg}
}

// transportFor builds the way to reach the internet through this outbound.
// Proxies are dialled as proxies; a WireGuard hop is reached by marking the
// socket the way customer traffic is marked, so the kernel routes it into the
// hop's tunnel; direct is the server's own route.
func transportFor(ob *model.Outbound) (*http.Transport, error) {
	base := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	}

	switch ob.Kind {
	case model.OutboundDirect:
		tr.DialContext = base.DialContext

	case model.OutboundWireGuard:
		if ob.Mark == 0 {
			return nil, errors.New("this hop has no routing mark yet")
		}
		d, err := markedDialer(base, ob.Mark)
		if err != nil {
			return nil, err
		}
		// The hop carries IPv4 only, and so does the rule that steers marked
		// packets into it. A v6 connection would take the server's own route
		// out and report the server's own address as the hop's.
		tr.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
			return d.DialContext(ctx, "tcp4", addr)
		}

	case model.OutboundSOCKS:
		var auth *proxy.Auth
		if ob.Username != "" {
			auth = &proxy.Auth{User: ob.Username, Password: ob.Password}
		}
		pd, err := proxy.SOCKS5("tcp", ob.Address, auth, base)
		if err != nil {
			return nil, err
		}
		cd, ok := pd.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("socks dialer does not take a context")
		}
		tr.DialContext = cd.DialContext

	case model.OutboundHTTP:
		u := &url.URL{Scheme: "http", Host: ob.Address}
		if ob.Username != "" {
			u.User = url.UserPassword(ob.Username, ob.Password)
		}
		tr.Proxy = http.ProxyURL(u)
		tr.DialContext = base.DialContext

	default:
		return nil, fmt.Errorf("nothing to send through a %s outbound", ob.Kind)
	}
	return tr, nil
}

// parseTrace reads the key=value lines the trace endpoint answers with.
func parseTrace(r io.Reader) (Egress, error) {
	var eg Egress
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "ip":
			eg.IPv4 = v
		case "loc":
			eg.Country = v
		}
	}
	if err := sc.Err(); err != nil {
		return eg, err
	}
	if eg.IPv4 == "" {
		return eg, errors.New("the trace did not say where the request came from")
	}
	return eg, nil
}
