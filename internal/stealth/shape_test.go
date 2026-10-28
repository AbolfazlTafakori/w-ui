package stealth

import "testing"

func given(method, place, mode string) Given {
	return Given{Method: method, Place: place, Mode: mode}
}

func TestPresetsAreTheShapesOfThePage(t *testing.T) {
	type want struct {
		method   string
		place    Place
		mode     Mode
		limit    int
		inflight int
	}
	cases := map[string]want{
		"post-stream": {"POST", InBody, ModeSse, 65536, 2},
		"get-stream":  {"GET", InQuery, ModeSse, 4000, 2},
		"put-poll":    {"PUT", InBody, ModePoll, 65536, 1},
		"get-poll":    {"GET", InQuery, ModePoll, 4000, 1},
	}
	for name, w := range cases {
		s, ok := Preset(name)
		if !ok {
			t.Fatalf("%s: no preset", name)
		}
		if s.Method != w.method || s.Place != w.place || s.Mode != w.mode || s.Limit != w.limit || s.Inflight != w.inflight {
			t.Errorf("%s: got %+v", name, s)
		}
		if s.Gather != (Range{10, 60}) || s.DownGather != (Range{0, 20}) || s.Life != (Range{120, 900}) || !s.Fallback {
			t.Errorf("%s: default ranges wrong: %+v", name, s)
		}
	}
	if _, ok := Preset("classic"); ok {
		t.Error("classic is no shape")
	}
	if _, ok := Preset(""); ok {
		t.Error("the empty name is no shape")
	}
	if a, _ := Preset("post-stream"); a.LineMaxFor() != ShapeLineMax {
		t.Error("a body limit above 4000 reads an 8192 line")
	}
	if a, _ := Preset("get-poll"); a.LineMaxFor() != ShapeLineSmall {
		t.Error("a 4000 limit reads a 4096 line")
	}
}

func TestMethodAndPlaceValidOrRefused(t *testing.T) {
	valid := [][2]string{
		{"GET", "query"}, {"GET", "path"}, {"GET", "header"},
		{"POST", "body"}, {"PUT", "body"}, {"PATCH", "body"},
		{"DELETE", "body"}, {"DELETE", "query"}, {"DELETE", "path"}, {"DELETE", "header"},
		{"PURGE", "body"}, {"ABC", "query"}, {"ABCDEFGHIJKLMNOP", "header"},
	}
	for _, c := range valid {
		g := given(c[0], c[1], "poll")
		if c[1] == "header" {
			n := "X-Trace"
			g.Name = &n
		}
		if _, err := Resolve(g); err != nil {
			t.Errorf("%s in %s refused: %v", c[0], c[1], err)
		}
	}
	refused := [][2]string{
		{"GET", "body"}, {"POST", "query"}, {"POST", "path"}, {"POST", "header"},
		{"PUT", "query"}, {"PUT", "header"}, {"PATCH", "path"},
		{"CONNECT", "body"}, {"TRACE", "query"}, {"HEAD", "query"}, {"OPTIONS", "body"},
		{"get", "query"}, {"Get", "query"}, {"AB", "body"}, {"ABCDEFGHIJKLMNOPQ", "body"},
		{"DELETE1", "body"}, {"DE LETE", "body"}, {"", "body"},
		{"DELETE", "cookie"}, {"DELETE", ""}, {"DELETE", "Body"},
	}
	for _, c := range refused {
		g := given(c[0], c[1], "poll")
		if c[1] == "header" {
			n := "X-Trace"
			g.Name = &n
		}
		if _, err := Resolve(g); err == nil {
			t.Errorf("%s in %s taken", c[0], c[1])
		}
	}
}

func TestHeaderNameRequiredAndRefused(t *testing.T) {
	with := func(place string, name *string) error {
		g := given("DELETE", place, "poll")
		g.Name = name
		_, err := Resolve(g)
		return err
	}
	s := func(v string) *string { return &v }
	long := ""
	for len(long) < 32 {
		long += "a"
	}
	for _, name := range []string{"X-Session-Hint", "x", long} {
		if err := with("header", s(name)); err != nil {
			t.Errorf("header %q refused: %v", name, err)
		}
	}
	if with("header", nil) == nil {
		t.Error("a header shape needs a name")
	}
	for _, place := range []string{"body", "query", "path"} {
		if with(place, s("X-A")) == nil {
			t.Errorf("%s took a name", place)
		}
	}
	bad := []string{"", long + "a", "X_A", "X A", "X:A", "Host", "host", "Content-Type",
		"Connection", "Accept", "TE", "Trailer", "Sec-Fetch-Mode", "sec-x",
		"Proxy-Authorization", "User-Agent", "Cookie", "Authorization"}
	for _, name := range bad {
		if with("header", s(name)) == nil {
			t.Errorf("header %q taken", name)
		}
	}
	for _, name := range []string{"Secure", "Proxy", "Hosts", "X-Host", "Sec", "Prox"} {
		if with("header", s(name)) != nil {
			t.Errorf("header %q refused but should pass", name)
		}
	}
}

func TestLimitBoundsByPlace(t *testing.T) {
	limit := func(place string, v uint64) (int, error) {
		g := given("DELETE", place, "poll")
		if place == "header" {
			n := "X-A"
			g.Name = &n
		}
		g.Limit = &v
		s, err := Resolve(g)
		return s.Limit, err
	}
	for _, c := range []struct {
		place  string
		lo, hi uint64
	}{{"query", 1024, 8000}, {"path", 1024, 8000}, {"header", 1024, 4096}, {"body", 1024, 65536}} {
		if v, err := limit(c.place, c.lo); err != nil || v != int(c.lo) {
			t.Errorf("%s lo: %d %v", c.place, v, err)
		}
		if v, err := limit(c.place, c.hi); err != nil || v != int(c.hi) {
			t.Errorf("%s hi: %d %v", c.place, v, err)
		}
		if _, err := limit(c.place, c.lo-1); err == nil {
			t.Errorf("%s under lo taken", c.place)
		}
		if _, err := limit(c.place, c.hi+1); err == nil {
			t.Errorf("%s over hi taken", c.place)
		}
	}
}

func TestInflightOneWithPollUpToFourWithSse(t *testing.T) {
	inflight := func(mode string, v uint64) error {
		g := given("GET", "query", mode)
		g.Inflight = &v
		_, err := Resolve(g)
		return err
	}
	if inflight("poll", 1) != nil || inflight("poll", 2) == nil || inflight("poll", 0) == nil {
		t.Error("poll is one in flight")
	}
	if inflight("sse", 1) != nil || inflight("sse", 4) != nil || inflight("sse", 5) == nil || inflight("sse", 0) == nil {
		t.Error("sse is one to four in flight")
	}
}

func TestStreamMembersSayNothingForPoll(t *testing.T) {
	g := given("GET", "query", "poll")
	dg := [2]uint64{0, 1}
	g.DownGather = &dg
	if _, err := Resolve(g); err == nil {
		t.Error("down.gather taken with poll")
	}
	g = given("GET", "query", "poll")
	f := false
	g.Fallback = &f
	if _, err := Resolve(g); err == nil {
		t.Error("fallback taken with poll")
	}
	g = given("GET", "query", "sse")
	g.Fallback = &f
	if s, err := Resolve(g); err != nil || s.Fallback {
		t.Error("sse fallback false not kept")
	}
	if _, err := Resolve(given("GET", "query", "stream")); err == nil {
		t.Error("an unknown mode taken")
	}
}

func TestParseShapeStrict(t *testing.T) {
	full := `{"up":{"method":"DELETE","in":"header","name":"X-Hint","limit":2000,"inflight":3,"gather":[5,80]},"down":{"mode":"sse","gather":[1,30],"life":[60,600],"fallback":false}}`
	s, err := ParseShape([]byte(full))
	if err != nil {
		t.Fatal(err)
	}
	if s.Method != "DELETE" || s.Place != InHeader || s.Name != "X-Hint" || s.Limit != 2000 || s.Inflight != 3 ||
		s.Gather != (Range{5, 80}) || s.Mode != ModeSse || s.DownGather != (Range{1, 30}) || s.Life != (Range{60, 600}) || s.Fallback {
		t.Fatalf("got %+v", s)
	}
	getPoll, _ := Preset("get-poll")
	if g, _ := ParseShape([]byte(`{"up":{"method":"GET","in":"query"},"down":{"mode":"poll"}}`)); g != getPoll {
		t.Error("the short get-poll did not resolve to the preset")
	}
	bad := []string{
		`{"up":{"method":"GET","in":"query"}}`,
		`{"down":{"mode":"poll"}}`,
		`{"up":{"in":"query"},"down":{"mode":"poll"}}`,
		`{"up":{"method":"GET","in":"query"},"down":{"mode":"poll"},"extra":1}`,
		`{"up":{"method":"GET","in":"query","extra":1},"down":{"mode":"poll"}}`,
		`{"up":{"method":"GET","in":"query","limit":"4000"},"down":{"mode":"poll"}}`,
		`{"up":{"method":"GET","in":"query","limit":4000.5},"down":{"mode":"poll"}}`,
		`{"up":{"method":"GET","in":"query","limit":-1},"down":{"mode":"poll"}}`,
		`{"up":{"method":"GET","in":"query","gather":[1]},"down":{"mode":"poll"}}`,
		`{"up":{"method":1,"in":"query"},"down":{"mode":"poll"}}`,
		`{"up":null,"down":{"mode":"poll"}}`,
		`[]`,
		`{"up":{"method":"GET","in":"body"},"down":{"mode":"poll"}}`,
		`{"up":{"method":"GET","in":"query"},"down":{"mode":"poll","life":[30,40]}}`,
	}
	for _, b := range bad {
		if _, err := ParseShape([]byte(b)); err == nil {
			t.Errorf("taken: %s", b)
		}
	}
}

func TestShapePrintsInOneLine(t *testing.T) {
	gp, _ := Preset("get-poll")
	if gp.String() != "GET in query, limit 4000, 1 in flight, gather 10 to 60 ms, poll" {
		t.Errorf("get-poll: %q", gp.String())
	}
	ps, _ := Preset("post-stream")
	want := "POST in body, limit 65536, 2 in flight, gather 10 to 60 ms, sse, gather 0 to 20 ms, life 120 to 900 s, falls back to polling"
	if ps.String() != want {
		t.Errorf("post-stream: %q", ps.String())
	}
}
