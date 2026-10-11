package reconciler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abolfazl/w-ui/internal/database/model"
	"github.com/abolfazl/w-ui/internal/logger"
	"github.com/abolfazl/w-ui/internal/notify"
)

func history(t *testing.T, r *Reconciler) []model.SharingEvent {
	t.Helper()
	got, err := r.SharingHistory(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A device on the report is written down; while it stays on, minute after
// minute, it is one record that grows, and it stays after the report has
// moved on and after the customer is deleted.
func TestASharedDeviceIsKeptInTheRecord(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	seedSharing(t, db, now)
	ctx := context.Background()
	r := &Reconciler{db: db, log: quietLog()}

	for _, addr := range []string{"203.0.113.7:1", "198.51.100.4:2", "192.0.2.9:3"} {
		if err := recordEndpoints(ctx, db, map[uint]string{1: addr}, nil, now); err != nil {
			t.Fatal(err)
		}
	}
	r.maybeRecordSharing(ctx, now)
	// A minute on, a fourth address: the same record, one more address.
	if err := recordEndpoints(ctx, db, map[uint]string{1: "192.0.2.77:4"}, nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	r.maybeRecordSharing(ctx, now.Add(time.Minute))

	got := history(t, r)
	if len(got) != 1 {
		t.Fatalf("records %+v, want one that grew", got)
	}
	ev := got[0]
	if ev.Deleted {
		t.Fatal("a customer that is still there is marked deleted")
	}
	if ev.Kind != model.SharingShared || ev.ClientName != "Ali" || ev.DeviceName != "Laptop" || ev.Source != model.SharingLive {
		t.Fatalf("record %+v", ev)
	}
	if ev.Addrs != "192.0.2.77,192.0.2.9,198.51.100.4,203.0.113.7" {
		t.Fatalf("addresses %q", ev.Addrs)
	}
	if !ev.LastSeen.Equal(now.Add(time.Minute)) {
		t.Fatalf("last seen %v, want the second sighting", ev.LastSeen)
	}

	// The customer is deleted, and the report has nothing: the record stays,
	// names and all.
	if err := db.Exec("DELETE FROM accounts").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM clients").Error; err != nil {
		t.Fatal(err)
	}
	r.maybeRecordSharing(ctx, now.Add(2*time.Hour))
	got = history(t, r)
	if len(got) != 1 || got[0].ClientName != "Ali" || got[0].DeviceName != "Laptop" || !got[0].Deleted {
		t.Fatalf("after the customer was deleted: %+v", got)
	}
}

// The same device again, long after: a second time, a second record.
func TestALaterTimeIsANewRecord(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	r := &Reconciler{db: db, log: quietLog()}
	t0 := time.Date(2031, 1, 1, 10, 0, 0, 0, time.UTC)
	base := model.SharingEvent{Kind: model.SharingShared, ClientName: "Ali", DeviceName: "Laptop", Source: model.SharingLive}
	first, again, later := base, base, base
	first.StartedAt, first.LastSeen, first.Addrs = t0, t0.Add(5*time.Minute), "1.1.1.1"
	again.StartedAt, again.LastSeen, again.Addrs = t0.Add(20*time.Minute), t0.Add(25*time.Minute), "2.2.2.2"
	later.StartedAt, later.LastSeen = t0.Add(5*time.Hour), t0.Add(5*time.Hour)
	for _, ev := range []model.SharingEvent{first, again, later} {
		if _, err := recordSharing(ctx, db, ev); err != nil {
			t.Fatal(err)
		}
	}
	got := history(t, r)
	if len(got) != 2 {
		t.Fatalf("records %+v, want two: one that went on, one hours later", got)
	}
	if got[1].Addrs != "1.1.1.1,2.2.2.2" || !got[1].LastSeen.Equal(t0.Add(25*time.Minute)) {
		t.Fatalf("the first record %+v", got[1])
	}
	// A hold is its own kind: it does not fold into the shared record.
	held := base
	held.Kind, held.StartedAt, held.LastSeen = model.SharingHeld, t0.Add(time.Minute), t0.Add(time.Minute)
	if _, err := recordSharing(ctx, db, held); err != nil {
		t.Fatal(err)
	}
	if got := history(t, r); len(got) != 3 {
		t.Fatalf("records %+v, want the hold on its own", got)
	}
	// Held again a minute on, now on a node with a plan of two: the same
	// record, saying so.
	heldAgain := held
	heldAgain.StartedAt, heldAgain.LastSeen = t0.Add(2*time.Minute), t0.Add(2*time.Minute)
	heldAgain.Limit, heldAgain.Place = 2, "node 3"
	if _, err := recordSharing(ctx, db, heldAgain); err != nil {
		t.Fatal(err)
	}
	got = history(t, r)
	if len(got) != 3 {
		t.Fatalf("records %+v, want the hold extended", got)
	}
	for _, ev := range got {
		if ev.Kind == model.SharingHeld && (ev.Limit != 2 || ev.Place != "node 3" || !ev.LastSeen.Equal(t0.Add(2*time.Minute))) {
			t.Fatalf("hold %+v", ev)
		}
	}
}

// A hold the log recorded becomes a record of the device it names.
func TestAHoldIsReadBackFromTheLog(t *testing.T) {
	at := time.Date(2026, 9, 5, 5, 19, 20, 0, time.UTC)
	accounts := map[string]model.Account{"Ali / Home\x00Phone / 2": {ID: 7, ClientID: 3}}
	e := logger.Entry{Time: at, Message: logHeldOff, Fields: map[string]any{
		"client": "Ali / Home", "device": "Phone / 2", "on": "here", "limit": "1", "until": "5:21AM",
	}}
	ev, ok := heldFromLog(e, accounts)
	if !ok {
		t.Fatal("not read")
	}
	if ev.Kind != model.SharingHeld || ev.AccountID != 7 || ev.ClientID != 3 || ev.Limit != 1 || ev.Place != "here" ||
		ev.Source != model.SharingFromLog || !ev.StartedAt.Equal(at) {
		t.Fatalf("record %+v", ev)
	}
	if _, ok := heldFromLog(logger.Entry{Time: at, Message: logHeldOff, Fields: map[string]any{"device": "x"}}, nil); ok {
		t.Fatal("a line without its customer was read")
	}
}

// From the connections the log recorded, a device is found shared by the
// report's own rule: three places inside ten minutes. Two places is a phone
// changing networks, and three spread over an afternoon is three trips.
func TestSharingIsFoundInTheConnectionsTheLogRecorded(t *testing.T) {
	t0 := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

	if got := sharedFromConnects("Ali", "Phone", []sighting{{at(0), "1.1.1.1"}, {at(3), "2.2.2.2"}, {at(5), "1.1.1.1"}}); len(got) != 0 {
		t.Fatalf("two places were taken for sharing: %+v", got)
	}
	if got := sharedFromConnects("Ali", "Phone", []sighting{{at(0), "1.1.1.1"}, {at(60), "2.2.2.2"}, {at(120), "3.3.3.3"}}); len(got) != 0 {
		t.Fatalf("three places hours apart were taken for sharing: %+v", got)
	}

	seen := []sighting{
		{at(4), "3.3.3.3"}, {at(0), "1.1.1.1"}, {at(2), "2.2.2.2"}, // out of order, as a map gives them
		{at(9), "4.4.4.4"},
		// The next day, again.
		{at(1440), "5.5.5.5"}, {at(1442), "6.6.6.6"}, {at(1443), "7.7.7.7"},
	}
	got := sharedFromConnects("Ali", "Phone", seen)
	if len(got) != 2 {
		t.Fatalf("records %+v, want one each day", got)
	}
	if got[0].Addrs != "1.1.1.1,2.2.2.2,3.3.3.3,4.4.4.4" || !got[0].StartedAt.Equal(at(0)) || !got[0].LastSeen.Equal(at(9)) {
		t.Fatalf("first day %+v", got[0])
	}
	if got[1].Addrs != "5.5.5.5,6.6.6.6,7.7.7.7" || got[1].Source != model.SharingFromLog {
		t.Fatalf("second day %+v", got[1])
	}
}

// The log names a device "customer / device", and either name can hold " / ".
func TestADeviceNameFromTheLogIsSplitWhereADeviceIs(t *testing.T) {
	accounts := map[string]model.Account{
		"Ali / Home\x00Phone":   {ID: 1},
		"Sara\x00Laptop / Work": {ID: 2},
	}
	cases := map[string][2]string{
		"Ali / Home / Phone":   {"Ali / Home", "Phone"},
		"Sara / Laptop / Work": {"Sara", "Laptop / Work"},
		"Gone / Tablet":        {"Gone", "Tablet"}, // deleted: the last split
		"NoSeparator":          {"", "NoSeparator"},
	}
	for in, want := range cases {
		c, d := splitDeviceName(in, accounts)
		if c != want[0] || d != want[1] {
			t.Errorf("%q -> %q / %q, want %q / %q", in, c, d, want[0], want[1])
		}
	}
}

// Reading the log back where it cannot be read says why, and is noted so it
// is not tried on every start; reading it again adds nothing twice.
func TestReadingTheLogBackIsNotedAndRepeatable(t *testing.T) {
	db := newTestDB(t)
	r := &Reconciler{db: db, log: quietLog()}
	ctx := context.Background()
	if _, ok := r.SharingImportStatus(); ok {
		t.Fatal("a status before anything was read")
	}
	r.ImportSharingOnce(ctx)
	st, ok := r.SharingImportStatus()
	if !ok || st.State == "" || st.At.IsZero() {
		t.Fatalf("status %+v %v", st, ok)
	}
	// On this machine there is no journal for the panel's unit: whatever it
	// says, it says it, rather than claiming a record it did not read.
	if st.State == "done" && (st.Held != 0 || st.Shared != 0) {
		t.Fatalf("read records from a journal the test did not write: %+v", st)
	}
	before := st.At
	r.ImportSharingOnce(ctx)
	if again, _ := r.SharingImportStatus(); !again.At.Equal(before) {
		t.Fatal("read the log again on its own")
	}
}

// The sharing notice goes once, when a device first shows on the report: not
// every minute it stays there. It went nowhere before; the kind existed and
// nothing sent it.
func TestTheSharingNoticeGoesOnceWhenItBegins(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		mu.Lock()
		sent = append(sent, body.Text)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tg.Close()

	n := notify.New(quietLog())
	n.SetConfig(notify.Config{Enabled: true, BotToken: "1:test", ChatID: "1", APIServer: tg.URL,
		Kinds: map[string]bool{string(notify.KindSharing): true}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n.Start(ctx)

	db := newTestDB(t)
	now := time.Now().UTC()
	seedSharing(t, db, now)
	r := &Reconciler{db: db, log: quietLog(), notifier: n}
	for _, addr := range []string{"203.0.113.7:1", "198.51.100.4:2", "192.0.2.9:3"} {
		if err := recordEndpoints(ctx, db, map[uint]string{1: addr}, nil, now); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		r.maybeRecordSharing(ctx, now.Add(time.Duration(i)*time.Minute))
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := len(sent)
		mu.Unlock()
		if got >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // room for a second one, were it sent
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("sent %d notices %q, want one", len(sent), sent)
	}
	if !strings.Contains(sent[0], "Ali / Laptop") || !strings.Contains(sent[0], "3 addresses") {
		t.Fatalf("notice %q", sent[0])
	}
}
