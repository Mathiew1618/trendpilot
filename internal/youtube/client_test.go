package youtube

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMostPopular(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("chart") != "mostPopular" || q.Get("regionCode") != "US" || q.Get("key") != "k" {
			t.Errorf("unexpected query %v", q)
		}
		w.Write([]byte(`{"items":[{"id":"v1","snippet":{"publishedAt":"2026-10-14T10:00:00Z",
			"channelId":"c1","title":"Hello #Fall","tags":["Autumn Vibes"]},
			"statistics":{"viewCount":"1200","likeCount":"30"}}]}`))
	}))
	defer srv.Close()

	c := New("k")
	c.Base = srv.URL
	vs, err := c.MostPopular(context.Background(), "US", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Views != 1200 || vs[0].Likes != 30 || vs[0].Comments != 0 || vs[0].Tags[0] != "Autumn Vibes" {
		t.Fatalf("bad parse: %+v", vs)
	}
}

func TestAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"error":{"code":403,"message":"quota exceeded"}}`))
	}))
	defer srv.Close()
	c := New("k")
	c.Base = srv.URL
	if _, err := c.Stats(context.Background(), []string{"a"}); err == nil {
		t.Fatal("want error")
	}
}

func TestMostPopularPages(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("pageToken") == "" {
			w.Write([]byte(`{"nextPageToken":"p2","items":[{"id":"a"},{"id":"b"}]}`))
			return
		}
		w.Write([]byte(`{"items":[{"id":"c"}]}`))
	}))
	defer srv.Close()
	c := New("k")
	c.Base = srv.URL
	vs, err := c.MostPopular(context.Background(), "US", "", 200)
	if err != nil || len(vs) != 3 || calls != 2 {
		t.Fatalf("vs=%d calls=%d err=%v", len(vs), calls, err)
	}
}
