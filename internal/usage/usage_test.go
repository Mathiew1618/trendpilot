package usage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransportCountsBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 5000)))
	}))
	defer srv.Close()
	c := &http.Client{Transport: Transport(http.DefaultTransport)}
	before := received.Load()
	resp, err := c.Post(srv.URL, "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if got := received.Load() - before; got != 5000 {
		t.Fatalf("counted %d bytes, want 5000", got)
	}
	if r := Snapshot(); r.Requests < 1 || r.MemoryMB <= 0 {
		t.Fatalf("report %+v", r)
	}
}
