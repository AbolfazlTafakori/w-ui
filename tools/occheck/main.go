// Command occheck runs the panel's RADIUS server for a real ocserv, with the
// hook that ends a user's other sessions before a new one is let in, for a
// check of how ocserv takes it. Temporary: the agent and tools/protosim take
// it over.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/abolfazl/w-ui/internal/radiusd"
)

type occUser struct {
	ID       int    `json:"ID"`
	Username string `json:"Username"`
}

func sessions(user string) []int {
	out, err := exec.Command("occtl", "--json", "show", "users").Output()
	if err != nil {
		return nil
	}
	var us []occUser
	if json.Unmarshal(out, &us) != nil {
		return nil
	}
	var ids []int
	for _, u := range us {
		if u.Username == user {
			ids = append(ids, u.ID)
		}
	}
	return ids
}

func main() {
	users := flag.String("users", "", "user:password:ip,...")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	known := map[string]radiusd.User{}
	for _, u := range strings.Split(*users, ",") {
		f := strings.SplitN(u, ":", 3)
		known[f[0]] = radiusd.User{Password: f[1], IP: netip.MustParseAddr(f[2]),
			DNS: []netip.Addr{netip.MustParseAddr("1.1.1.1")}}
	}
	srv, err := radiusd.Listen("127.0.0.1:1812", []byte("RadSecret9"), func(n string) (radiusd.User, bool) {
		u, ok := known[n]
		return u, ok
	}, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	srv.SetOnAccept(func(name string) {
		ids := sessions(name)
		for _, id := range ids {
			_ = exec.Command("occtl", "disconnect", "id", fmt.Sprint(id)).Run()
		}
		deadline := time.Now().Add(3 * time.Second)
		for len(ids) > 0 && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			ids = sessions(name)
		}
		log.Info("accepting", "user", name, "left", len(ids))
	})
	fmt.Println("radius ready")
	srv.Serve(context.Background())
}
