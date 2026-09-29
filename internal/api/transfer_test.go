package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// slowBody sends its content in pieces, pausing between them, like an
// archive going up over a slow link.
type slowBody struct {
	parts []string
	pause time.Duration
}

func (b *slowBody) Read(p []byte) (int, error) {
	if len(b.parts) == 0 {
		return 0, io.EOF
	}
	time.Sleep(b.pause)
	n := copy(p, b.parts[0])
	b.parts = b.parts[1:]
	return n, nil
}

// An archive that takes longer to arrive than the server gives an ordinary
// request is still read whole, through the same writer wrapper every handler
// here is given.
func TestASlowUploadOutlastsTheReadTimeout(t *testing.T) {
	for _, lifted := range []bool{false, true} {
		got := make(chan string, 1)
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w}
			if lifted {
				allowLongTransfer(rec)
			}
			b, _ := io.ReadAll(r.Body)
			got <- string(b)
		}))
		srv.Config.ReadTimeout = 150 * time.Millisecond
		srv.Start()

		body := &slowBody{parts: []string{"one ", "two ", "three ", "four"}, pause: 120 * time.Millisecond}
		req, _ := http.NewRequest(http.MethodPost, srv.URL, body)
		req.ContentLength = int64(len("one two three four"))
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		read := <-got
		srv.Close()

		whole := read == "one two three four"
		if lifted && !whole {
			t.Errorf("with the deadline lifted the upload arrived as %q", read)
		}
		if !lifted && whole {
			t.Error("the test proves nothing: the upload arrived whole without the deadline lifted")
		}
	}
}
