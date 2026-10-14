// Package obs talks to OBS Studio's built-in WebSocket server (v5, OBS 28+)
// to save the replay buffer when chat surges. Each call opens a short
// connection, so nothing stays attached to OBS during the stream.
package obs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	Addr     string // host:port, default localhost:4455
	Password string
}

type message struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}

var reqID atomic.Int64

// Auth computes obs-websocket's challenge response:
// base64(sha256(base64(sha256(password + salt)) + challenge)).
func Auth(password, salt, challenge string) string {
	s := sha256.Sum256([]byte(password + salt))
	secret := base64.StdEncoding.EncodeToString(s[:])
	a := sha256.Sum256([]byte(secret + challenge))
	return base64.StdEncoding.EncodeToString(a[:])
}

// Request opens a connection, identifies, sends one request and returns its
// responseData.
func (c *Client) Request(ctx context.Context, requestType string, data any) (json.RawMessage, error) {
	addr := c.Addr
	if addr == "" {
		addr = "localhost:4455"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, "ws://"+addr, nil)
	if err != nil {
		return nil, fmt.Errorf("can't reach OBS at %s (is OBS open with Tools → WebSocket Server Settings → Enable turned on?): %w", addr, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetReadDeadline(dl)
	}

	var hello struct {
		Auth *struct {
			Challenge string `json:"challenge"`
			Salt      string `json:"salt"`
		} `json:"authentication"`
	}
	if err := readOp(conn, 0, &hello); err != nil {
		return nil, err
	}
	ident := map[string]any{"rpcVersion": 1, "eventSubscriptions": 0}
	if hello.Auth != nil {
		if c.Password == "" {
			return nil, errors.New("OBS asks for a WebSocket password; add OBS_WEBSOCKET_PASSWORD")
		}
		ident["authentication"] = Auth(c.Password, hello.Auth.Salt, hello.Auth.Challenge)
	}
	if err := write(conn, 1, ident); err != nil {
		return nil, err
	}
	if err := readOp(conn, 2, nil); err != nil {
		return nil, fmt.Errorf("OBS refused the connection (wrong WebSocket password?): %w", err)
	}

	id := strconv.FormatInt(reqID.Add(1), 10)
	req := map[string]any{"requestType": requestType, "requestId": id}
	if data != nil {
		req["requestData"] = data
	}
	if err := write(conn, 6, req); err != nil {
		return nil, err
	}
	for {
		var resp struct {
			RequestID string `json:"requestId"`
			Status    struct {
				Result  bool   `json:"result"`
				Code    int    `json:"code"`
				Comment string `json:"comment"`
			} `json:"requestStatus"`
			Data json.RawMessage `json:"responseData"`
		}
		if err := readOp(conn, 7, &resp); err != nil {
			return nil, err
		}
		if resp.RequestID != id {
			continue
		}
		if !resp.Status.Result {
			return nil, fmt.Errorf("OBS %s failed (code %d) %s", requestType, resp.Status.Code, resp.Status.Comment)
		}
		return resp.Data, nil
	}
}

// SaveReplay saves OBS's replay buffer if it is running. ok=false means the
// replay buffer is off, which is not an error.
func (c *Client) SaveReplay(ctx context.Context) (ok bool, err error) {
	raw, err := c.Request(ctx, "GetReplayBufferStatus", nil)
	if err != nil {
		return false, err
	}
	var st struct {
		Active bool `json:"outputActive"`
	}
	json.Unmarshal(raw, &st)
	if !st.Active {
		return false, nil
	}
	_, err = c.Request(ctx, "SaveReplayBuffer", nil)
	return err == nil, err
}

func write(conn *websocket.Conn, op int, d any) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return conn.WriteJSON(message{Op: op, D: b})
}

// readOp reads until a message with the wanted op code arrives (events,
// op 5, are skipped).
func readOp(conn *websocket.Conn, op int, into any) error {
	for {
		var m message
		if err := conn.ReadJSON(&m); err != nil {
			return err
		}
		if m.Op != op {
			continue
		}
		if into != nil {
			return json.Unmarshal(m.D, into)
		}
		return nil
	}
}
