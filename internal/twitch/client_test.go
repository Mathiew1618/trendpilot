package twitch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fake(t *testing.T) (*Client, *int) {
	tokens := 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_secret") != "s" {
			w.WriteHeader(400)
			return
		}
		tokens++
		fmt.Fprintf(w, `{"access_token":"tok%d","expires_in":3600}`, tokens)
	})
	mux.HandleFunc("GET /helix/games/top", func(w http.ResponseWriter, r *http.Request) {
		// First token is "revoked" to exercise the 401 retry.
		if r.Header.Get("Authorization") == "Bearer tok1" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Query().Get("after") == "" {
			w.Write([]byte(`{"data":[{"id":"1","name":"Fortnite","igdb_id":"1905"}],"pagination":{"cursor":"c1"}}`))
		} else {
			w.Write([]byte(`{"data":[{"id":"2","name":"Just Chatting","igdb_id":""}],"pagination":{}}`))
		}
	})
	mux.HandleFunc("GET /helix/streams", func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		b.WriteString(`{"data":[`)
		for i := 0; i < 100; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"user_login":"u%d","game_id":"1","viewer_count":%d,"language":"en"}`, i, 1000-i)
		}
		b.WriteString(`],"pagination":{"cursor":"next"}}`)
		w.Write([]byte(b.String()))
	})
	mux.HandleFunc("POST /igdb/games", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "where id = (1905)") {
			t.Errorf("igdb body %q", body)
		}
		w.Write([]byte(`[{"id":1905,"genres":[{"name":"Shooter"},{"name":"Adventure"}]}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := New("id", "s")
	c.Helix, c.Auth, c.IGDB = srv.URL+"/helix", srv.URL+"/token", srv.URL+"/igdb"
	return c, &tokens
}

func TestTopGamesPaginatesAndRetries401(t *testing.T) {
	c, tokens := fake(t)
	gs, err := c.TopGames(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 2 || gs[1].Name != "Just Chatting" || *tokens != 2 {
		t.Fatalf("games %+v tokens %d", gs, *tokens)
	}
}

func TestStreamsCapsPages(t *testing.T) {
	c, _ := fake(t)
	ss, more, err := c.Streams(context.Background(), "1", "en", 2)
	if err != nil || len(ss) != 200 || !more {
		t.Fatalf("got %d streams more=%v err=%v", len(ss), more, err)
	}
}

func TestGenres(t *testing.T) {
	c, _ := fake(t)
	g, err := c.Genres(context.Background(), []string{"1905", ""})
	if err != nil || strings.Join(g["1905"], ",") != "Shooter,Adventure" {
		t.Fatalf("genres %v err %v", g, err)
	}
}
