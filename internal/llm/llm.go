// Package llm calls any OpenAI-compatible chat-completions endpoint. The
// default is Nous Portal using the OAuth token Hermes already manages.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

type StaticToken string

func (s StaticToken) Token(context.Context) (string, error) { return string(s), nil }

// HermesToken reads the Nous access token from Hermes' auth.json on every
// call. The token expires hourly, so it shells out to `hermes auth refresh`
// when it is within two minutes of expiry. Hermes owns the refresh token.
type HermesToken struct {
	Path     string // default ~/.hermes/auth.json
	Provider string // default "nous"
	mu       sync.Mutex
}

func (h *HermesToken) Token(ctx context.Context) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	tok, exp, err := h.read()
	if err != nil {
		return "", err
	}
	if time.Until(exp) < 2*time.Minute {
		cmd := exec.CommandContext(ctx, "hermes", "auth", "refresh", h.provider())
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("hermes auth refresh: %v: %s", err, out)
		}
		if tok, _, err = h.read(); err != nil {
			return "", err
		}
	}
	return tok, nil
}

func (h *HermesToken) provider() string {
	if h.Provider == "" {
		return "nous"
	}
	return h.Provider
}

func (h *HermesToken) read() (string, time.Time, error) {
	path := h.Path
	if path == "" {
		home, _ := os.UserHomeDir()
		path = home + "/.hermes/auth.json"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("no LLM key: set TRENDPILOT_LLM_API_KEY (and TRENDPILOT_LLM_BASE_URL for providers other than Nous); no Hermes login found at %s", path)
	}
	var f struct {
		Providers map[string]struct {
			AccessToken string    `json:"access_token"`
			ExpiresAt   time.Time `json:"expires_at"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return "", time.Time{}, fmt.Errorf("parse hermes auth: %w", err)
	}
	p, ok := f.Providers[h.provider()]
	if !ok || p.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("hermes has no %s login; run: hermes auth add nous --type oauth --no-browser", h.provider())
	}
	return p.AccessToken, p.ExpiresAt, nil
}

var UserAgent = "TrendPilot/dev"

type Client struct {
	BaseURL string
	Model   string
	Tokens  TokenSource
	HTTP    *http.Client
	sem     chan struct{} // bounds concurrent calls
}

// MaxConcurrent calls per client; more wait their turn, which keeps a
// small server's memory flat and a shared key under its rate limit.
const MaxConcurrent = 2

func New(baseURL, model string, tokens TokenSource) *Client {
	return &Client{BaseURL: baseURL, Model: model, Tokens: tokens, HTTP: &http.Client{Timeout: 90 * time.Second},
		sem: make(chan struct{}, MaxConcurrent)}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (c *Client) Chat(ctx context.Context, system, user string) (string, error) {
	if c.sem != nil {
		select {
		case c.sem <- struct{}{}:
			defer func() { <-c.sem }()
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	tok, err := c.Tokens.Token(ctx)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]any{
		"model":       c.Model,
		"messages":    []message{{"system", system}, {"user", user}},
		"temperature": 0.7,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	// Nous sits behind Cloudflare, which rejects Go's default User-Agent.
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: HTTP %d: %.300s", resp.StatusCode, raw)
	}
	var r struct {
		Choices []struct {
			Message message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("llm: decode: %w", err)
	}
	if len(r.Choices) == 0 {
		return "", fmt.Errorf("llm: empty response")
	}
	return r.Choices[0].Message.Content, nil
}
