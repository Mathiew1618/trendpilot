package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Scopes TrendPilot asks the creator for: making clips of their own stream,
// and starting a raid when they press the Raid button.
var Scopes = []string{"clips:edit", "channel:manage:raids"}

// UserToken is the creator's own Twitch sign-in, saved with 0600
// permissions next to the viewer history.
type UserToken struct {
	Access  string    `json:"access_token"`
	Refresh string    `json:"refresh_token"`
	Expires time.Time `json:"expires_at"`
	Scopes  []string  `json:"scopes"`
	UserID  string    `json:"user_id"`
	Login   string    `json:"login"`
}

type tokenResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ExpiresIn    int      `json:"expires_in"`
	Scope        []string `json:"scope"`
	Message      string   `json:"message"`
}

// DeviceLogin runs Twitch's device-code flow: show(uri, code) tells the
// creator where to approve, then this polls until they do (or the code
// expires). Nothing listens on a port.
func (c *Client) DeviceLogin(ctx context.Context, show func(uri, code string)) (*UserToken, error) {
	form := url.Values{"client_id": {c.ID}, "scopes": {strings.Join(Scopes, " ")}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.Auth, "/token")+"/device", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var dev struct {
		DeviceCode      string `json:"device_code"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Message         string `json:"message"`
	}
	status, err := c.do(req, &dev)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK || dev.DeviceCode == "" {
		return nil, fmt.Errorf("twitch device login: HTTP %d %s", status, dev.Message)
	}
	show(dev.VerificationURI, dev.UserCode)

	interval := max(time.Duration(dev.Interval)*time.Second, minDevicePoll)
	deadline := time.Now().Add(time.Duration(dev.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		tr, status, err := c.tokenRequest(ctx, url.Values{
			"client_id": {c.ID}, "client_secret": {c.Secret},
			"scopes": {strings.Join(Scopes, " ")}, "device_code": {dev.DeviceCode},
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
		})
		if err != nil {
			return nil, err
		}
		if status == http.StatusOK {
			tok := &UserToken{Access: tr.AccessToken, Refresh: tr.RefreshToken, Scopes: tr.Scope,
				Expires: time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)}
			if err := c.fillUser(ctx, tok); err != nil {
				return nil, err
			}
			return tok, nil
		}
		if !strings.Contains(tr.Message, "authorization_pending") && !strings.Contains(tr.Message, "slow_down") {
			return nil, fmt.Errorf("twitch login: %s", tr.Message)
		}
	}
	return nil, errors.New("twitch login: the code expired before it was approved")
}

func (c *Client) tokenRequest(ctx context.Context, form url.Values) (tokenResponse, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Auth, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var tr tokenResponse
	status, err := c.do(req, &tr)
	return tr, status, err
}

func (c *Client) fillUser(ctx context.Context, tok *UserToken) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.Helix+"/users", nil)
	req.Header.Set("Client-Id", c.ID)
	req.Header.Set("Authorization", "Bearer "+tok.Access)
	var p page[struct {
		ID    string `json:"id"`
		Login string `json:"login"`
	}]
	status, err := c.do(req, &p)
	if err != nil {
		return err
	}
	if status != http.StatusOK || len(p.Data) == 0 {
		return fmt.Errorf("twitch: couldn't read the signed-in user (HTTP %d)", status)
	}
	tok.UserID, tok.Login = p.Data[0].ID, p.Data[0].Login
	return nil
}

var minDevicePoll = 2 * time.Second

// UserAuth keeps the creator's token fresh and on disk.
type UserAuth struct {
	c    *Client
	path string
	mu   sync.Mutex
	tok  *UserToken
}

// LoadUserAuth reads a saved sign-in; it returns nil if there is none.
func (c *Client) LoadUserAuth(path string) *UserAuth {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var tok UserToken
	if json.Unmarshal(b, &tok) != nil || tok.Refresh == "" {
		return nil
	}
	return &UserAuth{c: c, path: path, tok: &tok}
}

func SaveUserToken(path string, tok *UserToken) error {
	b, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func (u *UserAuth) User() (id, login string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.tok.UserID, u.tok.Login
}

func (u *UserAuth) HasScope(s string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, x := range u.tok.Scopes {
		if x == s {
			return true
		}
	}
	return false
}

// Token returns a valid access token, refreshing it when force is set or
// it is about to expire.
func (u *UserAuth) Token(ctx context.Context, force bool) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !force && time.Until(u.tok.Expires) > 5*time.Minute {
		return u.tok.Access, nil
	}
	tr, status, err := u.c.tokenRequest(ctx, url.Values{
		"client_id": {u.c.ID}, "client_secret": {u.c.Secret},
		"grant_type": {"refresh_token"}, "refresh_token": {u.tok.Refresh},
	})
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("twitch sign-in expired (%s); run `trendpilot twitch-login` again", tr.Message)
	}
	u.tok.Access, u.tok.Expires = tr.AccessToken, time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second)
	if tr.RefreshToken != "" {
		u.tok.Refresh = tr.RefreshToken
	}
	if len(tr.Scope) > 0 {
		u.tok.Scopes = tr.Scope
	}
	return u.tok.Access, SaveUserToken(u.path, u.tok)
}

// asUser sends a Helix request with the creator's token, refreshing once on 401.
func (u *UserAuth) asUser(ctx context.Context, method, path string, q url.Values, out any) (int, error) {
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := u.Token(ctx, attempt > 0)
		if err != nil {
			return 0, err
		}
		req, err := http.NewRequestWithContext(ctx, method, u.c.Helix+path+"?"+q.Encode(), nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Client-Id", u.c.ID)
		req.Header.Set("Authorization", "Bearer "+tok)
		status, err := u.c.do(req, out)
		if err != nil || status != http.StatusUnauthorized {
			return status, err
		}
	}
	return http.StatusUnauthorized, errors.New("twitch: not authorized; sign in again")
}

// clipPoll is how often CreateClip checks whether Twitch has finished.
var clipPoll = 5 * time.Second

type Clip struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	EditURL string `json:"edit_url"`
	Title   string `json:"title"`
	Thumb   string `json:"thumbnail_url"`
}

// CreateClip clips the last ~30 seconds of the creator's live stream and
// waits (up to a minute) for Twitch to finish processing it.
func (u *UserAuth) CreateClip(ctx context.Context) (Clip, error) {
	id, _ := u.User()
	var created page[Clip]
	status, err := u.asUser(ctx, http.MethodPost, "/clips", url.Values{"broadcaster_id": {id}}, &created)
	if err != nil {
		return Clip{}, err
	}
	switch {
	case status == http.StatusNotFound:
		return Clip{}, errors.New("can't clip: the stream isn't live")
	case status == http.StatusForbidden:
		return Clip{}, errors.New("can't clip: clips are turned off or restricted on this channel")
	case status != http.StatusAccepted && status != http.StatusOK || len(created.Data) == 0:
		return Clip{}, fmt.Errorf("can't clip: Twitch answered HTTP %d", status)
	}
	clip := created.Data[0]
	for i := 0; i < 12; i++ {
		select {
		case <-ctx.Done():
			return clip, ctx.Err()
		case <-time.After(clipPoll):
		}
		var got page[Clip]
		if err := u.c.get(ctx, "/clips", url.Values{"id": {clip.ID}}, &got); err == nil && len(got.Data) > 0 {
			got.Data[0].EditURL = clip.EditURL
			return got.Data[0], nil
		}
	}
	return clip, errors.New("Twitch is still processing the clip; it may appear in a few minutes")
}

// StartRaid sends the creator's viewers to another live channel. Only
// called when the creator presses the Raid button.
func (u *UserAuth) StartRaid(ctx context.Context, toUserID string) error {
	id, _ := u.User()
	var out json.RawMessage
	status, err := u.asUser(ctx, http.MethodPost, "/raids", url.Values{"from_broadcaster_id": {id}, "to_broadcaster_id": {toUserID}}, &out)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusConflict:
		return errors.New("a raid is already in progress")
	case http.StatusTooManyRequests:
		return errors.New("too many raid requests; wait a minute")
	}
	return fmt.Errorf("raid failed: Twitch answered HTTP %d (is the stream live, and does the target accept raids?)", status)
}

// StreamByLogin returns the channel's live stream, or ok=false when offline.
func (c *Client) StreamByLogin(ctx context.Context, login string) (Stream, bool, error) {
	var p page[Stream]
	if err := c.get(ctx, "/streams", url.Values{"user_login": {login}}, &p); err != nil {
		return Stream{}, false, err
	}
	if len(p.Data) == 0 {
		return Stream{}, false, nil
	}
	return p.Data[0], true, nil
}
