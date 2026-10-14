package twitch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeviceLoginRefreshAndClip(t *testing.T) {
	clipPoll, minDevicePoll = time.Millisecond, time.Millisecond
	var polls, refreshes, clipChecks atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth2/device", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("scopes") != "clips:edit channel:manage:raids" {
			t.Errorf("scopes %q", r.Form.Get("scopes"))
		}
		w.Write([]byte(`{"device_code":"dc","expires_in":60,"interval":0,"user_code":"ABCD","verification_uri":"https://twitch.tv/activate"}`))
	})
	mux.HandleFunc("POST /oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.Form.Get("grant_type") {
		case "urn:ietf:params:oauth:grant-type:device_code":
			if polls.Add(1) < 3 {
				w.WriteHeader(400)
				w.Write([]byte(`{"status":400,"message":"authorization_pending"}`))
				return
			}
			w.Write([]byte(`{"access_token":"old","refresh_token":"r1","expires_in":3600,"scope":["clips:edit","channel:manage:raids"]}`))
		case "refresh_token":
			refreshes.Add(1)
			w.Write([]byte(`{"access_token":"new","refresh_token":"r2","expires_in":3600}`))
		}
	})
	mux.HandleFunc("GET /helix/users", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"42","login":"creator"}]}`))
	})
	mux.HandleFunc("POST /helix/clips", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(401) // the old token was revoked
			return
		}
		if r.URL.Query().Get("broadcaster_id") != "42" {
			t.Errorf("broadcaster %q", r.URL.Query().Get("broadcaster_id"))
		}
		w.WriteHeader(202)
		w.Write([]byte(`{"data":[{"id":"Clip1","edit_url":"https://clips.twitch.tv/Clip1/edit"}]}`))
	})
	mux.HandleFunc("POST /oauth2/apptoken", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("GET /helix/clips", func(w http.ResponseWriter, r *http.Request) {
		if clipChecks.Add(1) < 2 {
			w.Write([]byte(`{"data":[]}`)) // still processing
			return
		}
		w.Write([]byte(`{"data":[{"id":"Clip1","url":"https://clips.twitch.tv/Clip1","title":"gg"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New("id", "secret")
	c.Helix, c.Auth = srv.URL+"/helix", srv.URL+"/oauth2/token"
	c.token, c.expires = "app", time.Now().Add(time.Hour) // app token for GET /clips

	var shown string
	tok, err := c.DeviceLogin(context.Background(), func(uri, code string) { shown = code })
	if err != nil {
		t.Fatal(err)
	}
	if shown != "ABCD" || tok.UserID != "42" || tok.Login != "creator" || polls.Load() != 3 {
		t.Fatalf("tok %+v shown %q polls %d", tok, shown, polls.Load())
	}

	path := filepath.Join(t.TempDir(), "twitch-user.json")
	if err := SaveUserToken(path, tok); err != nil {
		t.Fatal(err)
	}
	u := c.LoadUserAuth(path)
	if u == nil || !u.HasScope("clips:edit") {
		t.Fatal("reload failed")
	}
	clip, err := u.CreateClip(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if clip.URL != "https://clips.twitch.tv/Clip1" || clip.EditURL == "" || refreshes.Load() != 1 {
		t.Fatalf("clip %+v refreshes %d", clip, refreshes.Load())
	}
	if again := c.LoadUserAuth(path); again.tok.Refresh != "r2" {
		t.Fatal("refreshed token should be saved")
	}
}
