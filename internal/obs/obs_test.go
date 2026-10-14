package obs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeOBS speaks just enough obs-websocket v5 for SaveReplay.
func fakeOBS(t *testing.T, password string, active bool, saved *int) *httptest.Server {
	up := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		send := func(op int, d any) { b, _ := json.Marshal(d); conn.WriteJSON(message{Op: op, D: b}) }
		hello := map[string]any{"rpcVersion": 1}
		if password != "" {
			hello["authentication"] = map[string]string{"challenge": "ch", "salt": "sa"}
		}
		send(0, hello)
		var m message
		conn.ReadJSON(&m)
		var ident struct {
			Auth string `json:"authentication"`
		}
		json.Unmarshal(m.D, &ident)
		if password != "" && ident.Auth != Auth(password, "sa", "ch") {
			conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4009, "auth"), deadline())
			return
		}
		send(2, map[string]any{"negotiatedRpcVersion": 1})
		send(5, map[string]any{"eventType": "noise"}) // events must be skipped
		conn.ReadJSON(&m)
		var req struct {
			Type string `json:"requestType"`
			ID   string `json:"requestId"`
		}
		json.Unmarshal(m.D, &req)
		resp := map[string]any{"requestType": req.Type, "requestId": req.ID, "requestStatus": map[string]any{"result": true, "code": 100}}
		switch req.Type {
		case "GetReplayBufferStatus":
			resp["responseData"] = map[string]bool{"outputActive": active}
		case "SaveReplayBuffer":
			*saved++
		}
		send(7, resp)
	}))
}

func TestSaveReplay(t *testing.T) {
	saved := 0
	srv := fakeOBS(t, "pw", true, &saved)
	defer srv.Close()
	c := &Client{Addr: strings.TrimPrefix(srv.URL, "http://"), Password: "pw"}
	ok, err := c.SaveReplay(context.Background())
	if err != nil || !ok || saved != 1 {
		t.Fatalf("ok=%v err=%v saved=%d", ok, err, saved)
	}

	c.Password = "wrong"
	if _, err := c.SaveReplay(context.Background()); err == nil {
		t.Fatal("wrong password should fail")
	}
}

func TestReplayBufferOff(t *testing.T) {
	saved := 0
	srv := fakeOBS(t, "", false, &saved)
	defer srv.Close()
	c := &Client{Addr: strings.TrimPrefix(srv.URL, "http://")}
	ok, err := c.SaveReplay(context.Background())
	if err != nil || ok || saved != 0 {
		t.Fatalf("buffer off: ok=%v err=%v saved=%d", ok, err, saved)
	}
}

func TestMissingPassword(t *testing.T) {
	saved := 0
	srv := fakeOBS(t, "pw", true, &saved)
	defer srv.Close()
	c := &Client{Addr: strings.TrimPrefix(srv.URL, "http://")}
	if _, err := c.SaveReplay(context.Background()); err == nil || !strings.Contains(err.Error(), "OBS_WEBSOCKET_PASSWORD") {
		t.Fatalf("err %v", err)
	}
}

func deadline() time.Time { return time.Now().Add(time.Second) }
