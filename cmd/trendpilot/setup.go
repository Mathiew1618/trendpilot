package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"trendpilot/internal/llm"
	"trendpilot/internal/obs"
	"trendpilot/internal/twitch"
	"trendpilot/internal/youtube"
)

// envPath is where setup saves settings: next to the program, so a
// double-click always finds them.
func envPath() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "trendpilot.env")
	}
	return "trendpilot.env"
}

type wizard struct {
	in   *bufio.Reader
	vals map[string]string
}

func (w *wizard) ask(prompt string) string {
	fmt.Print(prompt)
	line, _ := w.in.ReadString('\n')
	return strings.TrimSpace(line)
}

// askKey shows the current value (masked) and keeps it on Enter.
func (w *wizard) askKey(name, label string) string {
	cur := w.vals[name]
	hint := ""
	if cur != "" {
		hint = fmt.Sprintf(" [Enter keeps %s]", mask(cur))
	}
	if v := w.ask(fmt.Sprintf("  Paste your %s%s: ", label, hint)); v != "" {
		w.vals[name] = v
	}
	return w.vals[name]
}

func mask(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "…" + s[len(s)-4:]
}

func ok(msg string)   { fmt.Println("  ✅ " + msg) }
func warn(msg string) { fmt.Println("  ⚠️  " + msg) }

func section(n int, title string) {
	fmt.Printf("\n━━━ Step %d of 7: %s ━━━\n", n, title)
}

func setupCmd(ctx context.Context) error {
	path := envPath()
	w := &wizard{in: bufio.NewReader(os.Stdin), vals: readEnvFile(path)}
	fmt.Println(`
  TrendPilot setup
  ────────────────
  This takes about 15 minutes, once. Each step tells you where to get a
  free key, and checks it works before moving on. Press Enter to skip a
  step or keep what you entered before.`)

	// 1. Discord
	section(1, "Discord bot (how you talk to TrendPilot)")
	fmt.Println(`  1. Open https://discord.com/developers/applications and click "New Application".
  2. Name it TrendPilot, then open the "Bot" tab and click "Reset Token".
  3. Copy the token.`)
	var inviteURL string
	for {
		tok := w.askKey("DISCORD_BOT_TOKEN", "Discord bot token")
		if tok == "" {
			warn("Skipped. Without Discord you can still use the command line.")
			break
		}
		id, name, err := checkDiscord(ctx, tok)
		if err == nil {
			ok("Connected as " + name)
			inviteURL = "https://discord.com/oauth2/authorize?client_id=" + id + "&scope=bot+applications.commands&permissions=3072"
			break
		}
		warn(err.Error() + " Try again, or press Enter to skip.")
		delete(w.vals, "DISCORD_BOT_TOKEN")
	}

	// 2. YouTube
	section(2, "YouTube key (trending videos and hashtags)")
	fmt.Println(`  1. Open https://console.cloud.google.com/ and create a project (any name).
  2. Search for "YouTube Data API v3" in the top bar, open it, click "Enable".
  3. Go to "Credentials" → "Create credentials" → "API key", and copy it.`)
	for {
		key := w.askKey("YOUTUBE_API_KEY", "YouTube API key")
		if key == "" {
			warn("Skipped. YouTube trends, /suggest and /watch will be off.")
			break
		}
		if _, err := youtube.New(key).MostPopular(ctx, "US", "", 1); err == nil {
			ok("YouTube key works")
			break
		} else {
			warn(fmt.Sprintf("That key didn't work (%v). New keys can take a minute to activate.", err))
			delete(w.vals, "YOUTUBE_API_KEY")
		}
	}

	// 3. Twitch
	section(3, "Twitch app (which games are trending, best times to stream)")
	fmt.Println(`  1. Open https://dev.twitch.tv/console/apps and log in (Twitch may ask you to turn on 2-factor).
  2. Click "Register Your Application". Name: TrendPilot-<your name>.
     OAuth Redirect URL: https://localhost (click Add)   Category: Analytics Tool   Client Type: Confidential
  3. Click "Manage", copy the Client ID, then click "New Secret" and copy the secret.`)
	for {
		id := w.askKey("TWITCH_CLIENT_ID", "Twitch Client ID")
		if id == "" {
			warn("Skipped. Game trends and best times will be off.")
			break
		}
		secret := w.askKey("TWITCH_CLIENT_SECRET", "Twitch Client Secret")
		if _, err := twitch.New(id, secret).TopGames(ctx, 1); err == nil {
			ok("Twitch app works")
			break
		} else {
			warn(fmt.Sprintf("That didn't work (%v).", err))
			delete(w.vals, "TWITCH_CLIENT_SECRET")
		}
	}

	// 4. AI
	section(4, "AI writer (caption and hashtag ideas for /suggest)")
	fmt.Println(`  Pick one. Groq has a free tier and needs no credit card.
    1) Groq (free)       key at https://console.groq.com/keys
    2) OpenRouter        key at https://openrouter.ai/keys
    3) OpenAI            key at https://platform.openai.com/api-keys
    4) Ollama            free, runs on this computer (https://ollama.com)
    5) Skip`)
	providers := map[string][3]string{ // base URL, default model, key label
		"1": {"https://api.groq.com/openai/v1", "openai/gpt-oss-120b", "Groq API key"},
		"2": {"https://openrouter.ai/api/v1", "meta-llama/llama-3.3-70b-instruct", "OpenRouter API key"},
		"3": {"https://api.openai.com/v1", "gpt-4o-mini", "OpenAI API key"},
		"4": {"http://localhost:11434/v1", "llama3.1", ""},
	}
	for {
		choice := w.ask("  Choose 1-5: ")
		p, found := providers[choice]
		if !found {
			if choice != "5" && choice != "" {
				continue
			}
			warn("Skipped. Everything except /suggest works without it.")
			break
		}
		if w.vals["TRENDPILOT_LLM_BASE_URL"] != p[0] || w.vals["TRENDPILOT_LLM_MODEL"] == "" {
			w.vals["TRENDPILOT_LLM_MODEL"] = p[1] // new provider: its default model
		}
		w.vals["TRENDPILOT_LLM_BASE_URL"] = p[0]
		if p[2] == "" {
			w.vals["TRENDPILOT_LLM_API_KEY"] = "ollama"
		} else if w.askKey("TRENDPILOT_LLM_API_KEY", p[2]) == "" {
			continue
		}
		if m := w.ask(fmt.Sprintf("  Model [Enter for %s]: ", w.vals["TRENDPILOT_LLM_MODEL"])); m != "" {
			w.vals["TRENDPILOT_LLM_MODEL"] = m
		}
		c := llm.New(p[0], w.vals["TRENDPILOT_LLM_MODEL"], llm.StaticToken(w.vals["TRENDPILOT_LLM_API_KEY"]))
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		_, err := c.Chat(cctx, "Reply with the single word ok.", "ok?")
		cancel()
		if err == nil {
			ok("AI writer works")
			break
		}
		warn(fmt.Sprintf("That didn't work (%.200v). Check the key, or try another model name from the provider's model list.", err))
	}

	// 5. Where you are
	section(5, "Your region and time zone")
	region := w.ask(fmt.Sprintf("  Country code for trends, e.g. US, GB, CA, BR [Enter for %s]: ", or(w.vals["TRENDPILOT_REGION"], "US")))
	if region != "" {
		w.vals["TRENDPILOT_REGION"] = strings.ToUpper(region)
	}
	lang := w.ask("  Only count Twitch streams in your language? Two letters like en, es, pt [Enter for all]: ")
	if lang != "" {
		w.vals["TRENDPILOT_LANGUAGE"] = strings.ToLower(lang)
	}
	zone := detectZone()
	tz := w.ask(fmt.Sprintf("  Your time zone [Enter for %s]: ", or(w.vals["TRENDPILOT_TZ"], zone)))
	if tz == "" {
		tz = or(w.vals["TRENDPILOT_TZ"], zone)
	}
	if _, err := time.LoadLocation(tz); err != nil {
		warn("Unknown time zone; using " + zone + ". Names look like America/New_York or Europe/London.")
		tz = zone
	}
	w.vals["TRENDPILOT_TZ"] = tz

	// 6. The creator's own channel
	section(6, "Your Twitch channel (go-live posts, clips, raid suggestions)")
	fmt.Println(`  Optional. TrendPilot reads your chat (read-only) to spot big moments,
  and checks when you go live.`)
	if ch := w.ask(fmt.Sprintf("  Your Twitch channel name [Enter for %s]: ", or(w.vals["TWITCH_CHANNEL"], "skip"))); ch != "" {
		w.vals["TWITCH_CHANNEL"] = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ch), "@"))
	}
	if w.vals["TWITCH_CHANNEL"] != "" && w.vals["TWITCH_CLIENT_ID"] != "" && w.vals["TWITCH_CLIENT_SECRET"] != "" {
		ans := strings.ToLower(w.ask("  Sign in to Twitch so TrendPilot can make clips and raid with one click? (y/N): "))
		if strings.HasPrefix(ans, "y") {
			os.Setenv("TWITCH_CLIENT_ID", w.vals["TWITCH_CLIENT_ID"])
			os.Setenv("TWITCH_CLIENT_SECRET", w.vals["TWITCH_CLIENT_SECRET"])
			if err := twitchLoginCmd(ctx); err != nil {
				warn("Sign-in didn't finish (" + err.Error() + "). You can do it later with: trendpilot twitch-login")
			}
		}
	}

	// 7. OBS
	section(7, "OBS (optional: save a replay when chat goes wild)")
	fmt.Println(`  In OBS: Tools → WebSocket Server Settings → tick "Enable WebSocket server"
  → click "Show Connect Info" → copy the Server Password.
  For the replay to be saved, turn on Settings → Output → Replay Buffer.`)
	if pw := w.askKey("OBS_WEBSOCKET_PASSWORD", "OBS WebSocket password"); pw != "" {
		c := &obs.Client{Addr: w.vals["OBS_WEBSOCKET_ADDR"], Password: pw}
		if _, err := c.Request(ctx, "GetVersion", nil); err != nil {
			warn("Couldn't reach OBS right now (" + err.Error() + "). Saved anyway; it's checked again on each highlight.")
		} else {
			ok("OBS connected")
		}
	}

	if err := writeEnvFile(path, w.vals); err != nil {
		return fmt.Errorf("saving settings: %w", err)
	}
	fmt.Printf("\n  ✅ Settings saved to %s\n", path)
	if inviteURL != "" {
		fmt.Printf("\n  Last step: open this link to add the bot to your Discord server:\n\n    %s\n", inviteURL)
	}
	fmt.Println(`
  Start TrendPilot by double-clicking it again (or run: trendpilot run).
  Then, in Discord, choose where updates go (in the channel you want):
    /sendhere what:Trend alerts
    /sendhere what:Clips & highlights
    /sendhere what:Go-live announcements`)
	return nil
}

func checkDiscord(ctx context.Context, token string) (id, name string, err error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://discord.com/api/v10/oauth2/applications/@me", nil)
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/Mathiew1618/trendpilot, "+version+")")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("couldn't reach Discord: %v.", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", "", fmt.Errorf("Discord rejected that token. Copy it again after clicking Reset Token.")
	}
	var app struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&app) != nil {
		return "", "", fmt.Errorf("Discord answered HTTP %d.", resp.StatusCode)
	}
	return app.ID, app.Name, nil
}

func detectZone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	if runtime.GOOS != "windows" {
		if target, err := os.Readlink("/etc/localtime"); err == nil {
			if i := strings.Index(target, "zoneinfo/"); i >= 0 {
				return target[i+len("zoneinfo/"):]
			}
		}
	}
	return "UTC"
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func readEnvFile(path string) map[string]string {
	vals := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return vals
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		v = strings.TrimSpace(v)
		if q := v[:min(1, len(v))]; q == `"` || q == "'" {
			if end := strings.Index(v[1:], q); end >= 0 {
				v = v[1 : end+1] // quoted: keep '#' inside
			}
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i]) // trailing comment
		}
		vals[strings.TrimSpace(k)] = v
	}
	return vals
}

var envOrder = []struct{ key, comment string }{
	{"DISCORD_BOT_TOKEN", "Discord bot token"},
	{"DISCORD_GUILD_ID", "Optional: your server ID, so commands appear instantly"},
	{"YOUTUBE_API_KEY", "YouTube Data API v3 key"},
	{"TWITCH_CLIENT_ID", "Twitch app Client ID"},
	{"TWITCH_CLIENT_SECRET", "Twitch app Client Secret"},
	{"TRENDPILOT_LLM_BASE_URL", "AI provider (any OpenAI-compatible API)"},
	{"TRENDPILOT_LLM_MODEL", ""},
	{"TRENDPILOT_LLM_API_KEY", ""},
	{"TRENDPILOT_REGION", "Country for YouTube trends"},
	{"TRENDPILOT_LANGUAGE", "Only count Twitch streams in this language (empty = all)"},
	{"TRENDPILOT_TZ", "Your time zone, for best-time suggestions"},
	{"TWITCH_CHANNEL", "Your Twitch channel: go-live posts, chat highlights, raid suggestions"},
	{"OBS_WEBSOCKET_PASSWORD", "Optional: OBS WebSocket password, to save the replay buffer on highlights"},
	{"OBS_WEBSOCKET_ADDR", "Optional: OBS address if not this computer (default localhost:4455)"},
}

func writeEnvFile(path string, vals map[string]string) error {
	var b strings.Builder
	b.WriteString("# TrendPilot settings. Written by `trendpilot setup`; safe to edit.\n# Keep this file private: it holds your keys.\n\n")
	written := map[string]bool{}
	for _, e := range envOrder {
		if e.comment != "" {
			b.WriteString("# " + e.comment + "\n")
		}
		fmt.Fprintf(&b, "%s=%s\n", e.key, vals[e.key])
		written[e.key] = true
	}
	for k, v := range vals { // keep anything else the user added by hand
		if !written[k] {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
