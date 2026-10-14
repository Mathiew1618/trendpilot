// Command trendpilot is a trend-aware assistant for creators: it reads what
// is rising, suggests wording and hashtags, and watches how posts perform.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // IANA zones on Windows, which has no zoneinfo database

	"trendpilot/internal/app"
	"trendpilot/internal/discord"
	"trendpilot/internal/games"
	"trendpilot/internal/jobs"
	"trendpilot/internal/live"
	"trendpilot/internal/llm"
	"trendpilot/internal/monitor"
	"trendpilot/internal/obs"
	"trendpilot/internal/rewrite"
	"trendpilot/internal/server"
	"trendpilot/internal/store"
	"trendpilot/internal/trends"
	"trendpilot/internal/twitch"
	"trendpilot/internal/usage"
	"trendpilot/internal/youtube"
)

var version = "dev" // set by -ldflags at release

const helpText = `TrendPilot %s

Usage:
  trendpilot                        First time: setup wizard. After that: same as run.
  trendpilot setup                  Enter or change your keys
  trendpilot twitch-login           Sign in to your Twitch channel (clips, 1-click raids)
  trendpilot run                    Discord bot (if DISCORD_BOT_TOKEN is set) + monitor + local API
  trendpilot trends [-region US] [-category 20] [-n 15]
  trendpilot suggest -text "draft caption" [-platform youtube] [-niche "..."] [-region US]
  trendpilot games [-genre Shooter]   One Twitch + YouTube game-market scan
  trendpilot version

Settings come from environment variables or a trendpilot.env file in the
current folder or next to the program. See trendpilot.env.example.
`

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	loadEnvFile()
	llm.UserAgent = "TrendPilot/" + version
	http.DefaultTransport = usage.Transport(http.DefaultTransport)
	usage.Limit(envInt("TRENDPILOT_MAX_CPUS", 1), int64(envInt("TRENDPILOT_MAX_MEMORY_MB", 150)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Double-clicked (no arguments): first time, set up; afterwards, run.
	if len(os.Args) < 2 {
		var err error
		if _, statErr := os.Stat(envPath()); statErr != nil && os.Getenv("YOUTUBE_API_KEY") == "" && os.Getenv("TWITCH_CLIENT_ID") == "" {
			err = setupCmd(ctx)
		} else {
			fmt.Printf("TrendPilot %s is running. Leave this window open; close it or press Ctrl+C to stop.\n\n", version)
			err = run(ctx)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "\nerror:", err)
		}
		pauseIfDoubleClicked()
		if err != nil {
			os.Exit(1)
		}
		return
	}

	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "setup":
		err = setupCmd(ctx)
	case "twitch-login":
		err = twitchLoginCmd(ctx)
	case "run":
		err = run(ctx)
	case "trends":
		err = trendsCmd(ctx, args)
	case "suggest":
		err = suggestCmd(ctx, args)
	case "games":
		err = gamesCmd(ctx, args)
	case "version", "-v", "--version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, helpText, version)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	var n int
	if _, err := fmt.Sscan(env(k, ""), &n); err != nil {
		return def
	}
	return n
}

// pauseIfDoubleClicked keeps the console window open on Windows so a
// double-click user can read the last message before it disappears.
func pauseIfDoubleClicked() {
	if runtime.GOOS == "windows" {
		fmt.Print("\nPress Enter to close this window...")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
}

// loadEnvFile reads KEY=VALUE lines from trendpilot.env (current folder,
// then next to the program) without overriding variables already set.
func loadEnvFile() {
	paths := []string{"trendpilot.env", envPath()}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		for k, v := range readEnvFile(p) {
			if _, set := os.LookupEnv(k); !set && v != "" {
				os.Setenv(k, v)
			}
		}
		return
	}
}

func newLLM() *llm.Client {
	base := env("TRENDPILOT_LLM_BASE_URL", "https://inference-api.nousresearch.com/v1")
	model := env("TRENDPILOT_LLM_MODEL", "upstage/solar-mini4:free")
	var tok llm.TokenSource = &llm.HermesToken{}
	if k := env("TRENDPILOT_LLM_API_KEY", ""); k != "" {
		tok = llm.StaticToken(k)
	}
	return llm.New(strings.TrimRight(base, "/"), model, tok)
}

// build wires the agents. notify receives monitor alerts.
func build(notify monitor.Notifier) (*app.App, *twitch.Client, error) {
	yt := youtube.New(env("YOUTUBE_API_KEY", ""))
	mon := monitor.New(yt, notify)
	region := strings.ToUpper(env("TRENDPILOT_REGION", "US"))
	a := app.New(region, yt, rewrite.New(newLLM()), mon)

	id, secret := env("TWITCH_CLIENT_ID", ""), env("TWITCH_CLIENT_SECRET", "")
	if id == "" || secret == "" {
		return a, nil, nil
	}
	tw := twitch.New(id, secret)
	tw.UserAgent = llm.UserAgent
	loc, err := time.LoadLocation(env("TRENDPILOT_TZ", "Local"))
	if err != nil {
		return nil, nil, fmt.Errorf("TRENDPILOT_TZ: %w (use a name like America/New_York)", err)
	}
	var ytSrc games.YouTubeSource
	if yt.Key != "" {
		ytSrc = yt
	}
	g := games.NewService(tw, ytSrc, games.NewHistory(historyPath()))
	g.TopN = envInt("TRENDPILOT_GAMES_TOP", 30)
	g.Language = strings.ToLower(env("TRENDPILOT_LANGUAGE", ""))
	g.Region = region
	g.Loc = loc
	a.Games = g
	return a, tw, nil
}

// configDir holds TrendPilot's small files: viewer history, Discord
// channel settings and the creator's Twitch sign-in.
func configDir() string {
	if d := env("TRENDPILOT_DATA_DIR", ""); d != "" && !strings.EqualFold(d, "off") {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "."
	}
	return filepath.Join(base, "trendpilot")
}

// historyPath is where hour-of-week viewer averages are kept between runs.
// TRENDPILOT_DATA_DIR=off keeps them in memory only.
func historyPath() string {
	if strings.EqualFold(env("TRENDPILOT_DATA_DIR", ""), "off") {
		return ""
	}
	return filepath.Join(configDir(), "history.json")
}

func userTokenPath() string { return filepath.Join(configDir(), "twitch-user.json") }

func run(ctx context.Context) error {
	if env("YOUTUBE_API_KEY", "") == "" && env("TWITCH_CLIENT_ID", "") == "" {
		return errors.New("set YOUTUBE_API_KEY and/or TWITCH_CLIENT_ID + TWITCH_CLIENT_SECRET in trendpilot.env (see SETUP.md)")
	}
	every, err := time.ParseDuration(env("TRENDPILOT_POLL", "5m"))
	if err != nil || every < time.Minute {
		return fmt.Errorf("TRENDPILOT_POLL must be a duration of at least 1m, got %q", env("TRENDPILOT_POLL", ""))
	}
	scanEvery, err := time.ParseDuration(env("TRENDPILOT_SCAN", "10m"))
	if err != nil || scanEvery < 2*time.Minute {
		return fmt.Errorf("TRENDPILOT_SCAN must be a duration of at least 2m, got %q", env("TRENDPILOT_SCAN", ""))
	}
	shiftWindow, err := time.ParseDuration(env("TRENDPILOT_SHIFT_WINDOW", "20m"))
	if err != nil || shiftWindow < scanEvery {
		return fmt.Errorf("TRENDPILOT_SHIFT_WINDOW must be a duration of at least TRENDPILOT_SCAN, got %q", env("TRENDPILOT_SHIFT_WINDOW", ""))
	}

	var bot *discord.Bot
	webhook := env("TRENDPILOT_WEBHOOK", "")
	notify := func(owner string, a monitor.Alert) {
		slog.Info("alert", "kind", a.Kind, "video", a.VideoID, "msg", a.Message)
		if bot != nil && owner != "" {
			bot.Notify(owner, a)
		}
		if webhook != "" {
			postWebhook(webhook, a)
		}
	}
	a, tw, err := build(notify)
	if err != nil {
		return err
	}
	settings := store.Open(filepath.Join(configDir(), "settings.json"))
	var poster live.Poster = logPoster{}
	if tok := env("DISCORD_BOT_TOKEN", ""); tok != "" {
		if bot, err = discord.New(tok, env("DISCORD_GUILD_ID", ""), a); err != nil {
			return err
		}
		bot.Store = settings
		poster = bot
	}

	sched := jobs.New()
	if env("YOUTUBE_API_KEY", "") != "" {
		sched.Add(jobs.Job{Name: "watch-videos", Every: every, Delay: 5 * time.Second, Run: a.Monitor.Poll})
		sched.Add(jobs.Job{Name: "youtube-trends", Every: app.TrendTTL, Delay: 2 * time.Second, Run: a.RefreshOnce})
	} else {
		slog.Info("YOUTUBE_API_KEY not set; YouTube trends and /watch are off")
	}
	if a.Games != nil {
		sched.Add(jobs.Job{Name: "game-scan", Every: scanEvery, Run: func(ctx context.Context) error {
			if _, err := a.Games.Scan(ctx); err != nil {
				return err
			}
			if bot != nil {
				bot.PostShifts(a.Games.NewShifts(shiftWindow, 2*time.Hour, games.DefaultRules))
			}
			return nil
		}})
		sched.Add(jobs.Job{Name: "save-history", Every: time.Hour, Delay: time.Hour, Run: func(context.Context) error { return a.Games.Hist.Save() }})
		defer func() {
			if err := a.Games.Hist.Save(); err != nil {
				slog.Warn("saving history failed", "err", err)
			}
		}()
		slog.Info("game market scan on", "every", scanEvery, "categories", a.Games.TopN, "history", historyPath())
	} else {
		slog.Info("TWITCH_CLIENT_ID/SECRET not set; game trends are off")
	}

	if channel := env("TWITCH_CHANNEL", ""); channel != "" && tw != nil {
		user := tw.LoadUserAuth(userTokenPath())
		var clipper live.Clipper
		if user != nil && user.HasScope("clips:edit") {
			clipper = user
		}
		var replay live.Replayer
		if pw, addr := env("OBS_WEBSOCKET_PASSWORD", ""), env("OBS_WEBSOCKET_ADDR", ""); pw != "" || addr != "" {
			replay = &obs.Client{Addr: addr, Password: pw}
		}
		trendsFn := func() []trends.Trend {
			tctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ts, _ := a.Trends(tctx, "", "")
			return ts
		}
		w := live.NewWatcher(channel, tw, poster, newLLM(), clipper, replay, trendsFn)
		sched.Add(jobs.Job{Name: "stream-status", Every: time.Minute, Delay: 3 * time.Second, Run: w.Poll})
		go w.RunChat(ctx)
		if bot != nil {
			bot.Watcher, bot.User = w, user
		}
		slog.Info("following your stream", "channel", channel, "clips", clipper != nil, "obs", replay != nil)
	}
	// Hosted: every Discord server can follow its own creator (/mychannel).
	if bot != nil && tw != nil {
		writer := newLLM()
		trendsFn := func() []trends.Trend {
			tctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ts, _ := a.Trends(tctx, "", "")
			return ts
		}
		mgr := live.NewManager(ctx, tw, func(guildID, channel string) *live.Watcher {
			w := live.NewWatcher(channel, tw, bot.PosterFor(guildID), writer, nil, nil, trendsFn)
			if u := bot.UserFor(guildID); u != nil && u.HasScope("clips:edit") {
				if _, login := u.User(); strings.EqualFold(login, channel) {
					w.SetClipper(u)
				}
			}
			return w
		})
		bot.Live, bot.Twitch, bot.TokenDir = mgr, tw, filepath.Join(configDir(), "twitch-users")
		bot.SetSuggestLimit(20*time.Second, envInt("TRENDPILOT_SUGGEST_PER_DAY", 50))
		settings.Each(func(guildID string, g store.Guild) {
			if g.TwitchChannel != "" {
				mgr.Set(guildID, g.TwitchChannel)
			}
		})
		sched.Add(jobs.Job{Name: "creator-streams", Every: time.Minute, Delay: 4 * time.Second, Run: mgr.PollAll})
		slog.Info("per-server creators on", "following", mgr.Len())
	}
	sched.Add(jobs.Job{Name: "usage-report", Every: time.Hour, Delay: time.Hour, Run: logUsage})
	sched.Start(ctx)

	addr := env("TRENDPILOT_ADDR", "127.0.0.1:8790")
	mux := http.NewServeMux()
	mux.Handle("/", server.Handler(a))
	mux.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sched.Status())
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	errc := make(chan error, 2)
	go func() {
		slog.Info("local API listening", "addr", "http://"+addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	if bot != nil {
		go func() { errc <- bot.Run(ctx) }()
	} else {
		slog.Info("DISCORD_BOT_TOKEN not set; running without the Discord bot")
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		return err
	}
}

// logPoster stands in for Discord: stream posts go to the log.
type logPoster struct{}

func (logPoster) GoLive(_ context.Context, text string) { slog.Info("go-live post", "text", text) }
func (logPoster) Recap(_ context.Context, text string)  { slog.Info("stream recap", "text", text) }
func (logPoster) Queue(_ context.Context, it *live.QueueItem) {
	slog.Info("highlight", "title", it.Title, "clip", it.ClipURL, "at", it.Offset)
}

// twitchLoginCmd signs the creator in to Twitch for clips and raids.
func twitchLoginCmd(ctx context.Context) error {
	id, secret := env("TWITCH_CLIENT_ID", ""), env("TWITCH_CLIENT_SECRET", "")
	if id == "" || secret == "" {
		return errors.New("set up the Twitch app first (run: trendpilot setup)")
	}
	tw := twitch.New(id, secret)
	tw.UserAgent = llm.UserAgent
	tok, err := tw.DeviceLogin(ctx, func(uri, code string) {
		fmt.Printf("\n  1. Open %s\n  2. Check the code matches: %s\n  3. Click Authorize.\n\n  Waiting for you...\n", uri, code)
	})
	if err != nil {
		return err
	}
	if err := twitch.SaveUserToken(userTokenPath(), tok); err != nil {
		return err
	}
	fmt.Printf("  ✅ Signed in as %s. Clips and 1-click raids are on.\n", tok.Login)
	return nil
}

// logUsage writes what TrendPilot is costing the machine to its window,
// so a streamer can check it never competes with their stream.
func logUsage(context.Context) error {
	r := usage.Snapshot()
	slog.Info("usage", "cpu_pct_of_one_core", r.CPUPercent, "memory_mb", r.MemoryMB,
		"mb_per_hour", r.MBPerHour, "avg_kbps", r.AvgKbps, "api_requests", r.Requests)
	return nil
}

func postWebhook(url string, a monitor.Alert) {
	b, _ := json.Marshal(a)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(req); err != nil {
		slog.Warn("webhook failed", "err", err)
	} else {
		resp.Body.Close()
	}
}

func trendsCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("trends", flag.ExitOnError)
	region := fs.String("region", "", "two-letter country code (default TRENDPILOT_REGION or US)")
	category := fs.String("category", "", "YouTube category ID")
	n := fs.Int("n", 15, "how many to show")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Parse(args)

	a, _, err := build(func(string, monitor.Alert) {})
	if err != nil {
		return err
	}
	ts, err := a.Trends(ctx, strings.ToUpper(*region), *category)
	if err != nil {
		return err
	}
	ts = ts[:min(len(ts), *n)]
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(ts)
	}
	fmt.Printf("%-4s %-28s %7s %8s %8s  %s\n", "#", "term", "score", "chans", "crowd", "example")
	for i, t := range ts {
		ex := ""
		if len(t.Examples) > 0 {
			ex = t.Examples[0]
		}
		fmt.Printf("%-4d %-28s %7.1f %8d %7.0f%%  %.50s\n", i+1, t.Term, t.Score, t.Channels, t.Crowding*100, ex)
		if len(t.Related) > 0 {
			fmt.Printf("     related: %s\n", strings.Join(t.Related, " "))
		}
	}
	return nil
}

func suggestCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("suggest", flag.ExitOnError)
	text := fs.String("text", "", "draft caption or title (required)")
	platform := fs.String("platform", "youtube", "youtube, tiktok, instagram or twitch")
	niche := fs.String("niche", "", "creator niche")
	region := fs.String("region", "", "two-letter country code")
	fs.Parse(args)
	if *text == "" {
		return errors.New("-text is required")
	}
	a, _, err := build(func(string, monitor.Alert) {})
	if err != nil {
		return err
	}
	s, err := a.Suggest(ctx, rewrite.Draft{Text: *text, Platform: *platform, Niche: *niche}, strings.ToUpper(*region))
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

func gamesCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("games", flag.ExitOnError)
	genre := fs.String("genre", "", "only games in this genre")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Parse(args)
	a, _, err := build(func(string, monitor.Alert) {})
	if err != nil {
		return err
	}
	if a.Games == nil {
		return errors.New("TWITCH_CLIENT_ID and TWITCH_CLIENT_SECRET are required (see SETUP.md)")
	}
	start := time.Now()
	snap, err := a.Games.Scan(ctx)
	if err != nil {
		return err
	}
	if err := a.Games.Hist.Save(); err != nil {
		slog.Warn("saving history failed", "err", err)
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"snapshot": snap, "genres": games.Genres(snap), "opportunities": a.Games.Opportunities(*genre)})
	}
	fmt.Printf("%-32s %9s %9s %9s %6s  %s\n", "game", "viewers", "channels", "top25bar", "ytTrnd", "genres")
	for _, g := range snap.Games {
		if *genre != "" && !strings.Contains(strings.ToLower(strings.Join(g.Genres, ",")), strings.ToLower(*genre)) {
			continue
		}
		ch := fmt.Sprint(g.Channels)
		if g.More {
			ch += "+"
		}
		fmt.Printf("%-32.32s %9d %9s %9d %6d  %s\n", g.Name, g.Viewers, ch, g.EntryBar, g.YTVideos, strings.Join(g.Genres, "/"))
	}
	fmt.Println("\nBest openings (viewers per top-25 bar):")
	ops := a.Games.Opportunities(*genre)
	for i, g := range ops[:min(8, len(ops))] {
		fmt.Printf("%2d. %-32.32s %8.1f\n", i+1, g.Name, g.Opportunity)
	}
	fmt.Println("\nGenres:")
	for _, g := range games.Genres(snap) {
		fmt.Printf("  %-28s %9d\n", g.Name, g.Viewers)
	}
	r := usage.Snapshot()
	fmt.Printf("\nScan took %s: %d API requests, %.2f MB downloaded, %.2f MB uploaded.\n",
		time.Since(start).Round(time.Millisecond), r.Requests, r.DownloadedMB, r.UploadedMB)
	return nil
}
