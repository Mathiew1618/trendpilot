// Package twitch is a minimal Helix + IGDB client using an app access token
// (client-credentials flow). It only reads public data.
package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Game struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	IGDBID string `json:"igdb_id"`
}

type Stream struct {
	UserLogin   string    `json:"user_login"`
	GameID      string    `json:"game_id"`
	GameName    string    `json:"game_name"`
	ViewerCount int       `json:"viewer_count"`
	StartedAt   time.Time `json:"started_at"`
	Language    string    `json:"language"`
}

type Client struct {
	ID, Secret string
	Helix      string
	Auth       string
	IGDB       string
	HTTP       *http.Client
	UserAgent  string

	mu      sync.Mutex
	token   string
	expires time.Time
}

func New(id, secret string) *Client {
	return &Client{
		ID: id, Secret: secret,
		Helix: "https://api.twitch.tv/helix",
		Auth:  "https://id.twitch.tv/oauth2/token",
		IGDB:  "https://api.igdb.com/v4",
		HTTP:  &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) appToken(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.token != "" && time.Until(c.expires) > 5*time.Minute {
		return c.token, nil
	}
	form := url.Values{"client_id": {c.ID}, "client_secret": {c.Secret}, "grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Auth, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var r struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Message     string `json:"message"`
	}
	status, err := c.do(req, &r)
	if err != nil {
		return "", fmt.Errorf("twitch auth: %w", err)
	}
	if status != http.StatusOK || r.AccessToken == "" {
		return "", fmt.Errorf("twitch auth: HTTP %d %s (check TWITCH_CLIENT_ID / TWITCH_CLIENT_SECRET)", status, r.Message)
	}
	c.token = r.AccessToken
	c.expires = time.Now().Add(time.Duration(r.ExpiresIn) * time.Second)
	return c.token, nil
}

// authed sends a request with the app token, retrying once with a fresh
// token on 401.
func (c *Client) authed(ctx context.Context, build func() (*http.Request, error), out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := c.appToken(ctx, attempt > 0)
		if err != nil {
			return err
		}
		req, err := build()
		if err != nil {
			return err
		}
		req.Header.Set("Client-Id", c.ID)
		req.Header.Set("Authorization", "Bearer "+tok)
		status, err := c.do(req, out)
		if err != nil {
			return err
		}
		switch {
		case status == http.StatusUnauthorized && attempt == 0:
			continue
		case status == http.StatusTooManyRequests:
			return fmt.Errorf("twitch: rate limited (HTTP 429)")
		case status != http.StatusOK:
			return fmt.Errorf("twitch: HTTP %d from %s", status, req.URL.Path)
		}
		return nil
	}
	return fmt.Errorf("twitch: unauthorized")
}

func (c *Client) do(req *http.Request, out any) (int, error) {
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode %s: %w", req.URL.Path, err)
		}
	} else {
		json.Unmarshal(body, out) // best effort, for error messages
	}
	return resp.StatusCode, nil
}

type page[T any] struct {
	Data       []T `json:"data"`
	Pagination struct {
		Cursor string `json:"cursor"`
	} `json:"pagination"`
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	return c.authed(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, c.Helix+path+"?"+q.Encode(), nil)
	}, out)
}

// TopGames returns the n categories with the most viewers right now.
func (c *Client) TopGames(ctx context.Context, n int) ([]Game, error) {
	var out []Game
	cursor := ""
	for len(out) < n {
		q := url.Values{"first": {strconv.Itoa(min(100, n-len(out)))}}
		if cursor != "" {
			q.Set("after", cursor)
		}
		var p page[Game]
		if err := c.get(ctx, "/games/top", q, &p); err != nil {
			return out, err
		}
		out = append(out, p.Data...)
		if p.Pagination.Cursor == "" || len(p.Data) == 0 {
			break
		}
		cursor = p.Pagination.Cursor
	}
	return out[:min(len(out), n)], nil
}

// Streams returns up to pages*100 live streams for a game, most viewers
// first. language filters by broadcaster language ("" = all).
func (c *Client) Streams(ctx context.Context, gameID, language string, pages int) (streams []Stream, more bool, err error) {
	cursor := ""
	for i := 0; i < pages; i++ {
		q := url.Values{"game_id": {gameID}, "first": {"100"}, "type": {"live"}}
		if language != "" {
			q.Set("language", language)
		}
		if cursor != "" {
			q.Set("after", cursor)
		}
		var p page[Stream]
		if err := c.get(ctx, "/streams", q, &p); err != nil {
			return streams, false, err
		}
		streams = append(streams, p.Data...)
		cursor = p.Pagination.Cursor
		if cursor == "" || len(p.Data) < 100 {
			return streams, false, nil
		}
	}
	return streams, true, nil
}

// Genres looks up IGDB genre names for the given IGDB game IDs.
func (c *Client) Genres(ctx context.Context, igdbIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	var ids []string
	for _, id := range igdbIDs {
		if _, err := strconv.Atoi(id); err == nil {
			ids = append(ids, id)
		}
	}
	for start := 0; start < len(ids); start += 400 {
		chunk := ids[start:min(start+400, len(ids))]
		body := fmt.Sprintf("fields id,genres.name; where id = (%s); limit 500;", strings.Join(chunk, ","))
		var rows []struct {
			ID     int `json:"id"`
			Genres []struct {
				Name string `json:"name"`
			} `json:"genres"`
		}
		err := c.authed(ctx, func() (*http.Request, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.IGDB+"/games", strings.NewReader(body))
			if err == nil {
				req.Header.Set("Accept", "application/json")
			}
			return req, err
		}, &rows)
		if err != nil {
			return out, fmt.Errorf("igdb: %w", err)
		}
		for _, r := range rows {
			for _, g := range r.Genres {
				out[strconv.Itoa(r.ID)] = append(out[strconv.Itoa(r.ID)], g.Name)
			}
		}
	}
	return out, nil
}
