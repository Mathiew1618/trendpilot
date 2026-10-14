# Developing TrendPilot

Go 1.22+. One binary, two dependencies (discordgo, x/sys).

```bash
cp trendpilot.env.example trendpilot.env      # or: go run ./cmd/trendpilot setup
go test ./...
TRENDPILOT_LIVE=1 go test ./internal/rewrite -run Live -v   # real LLM call
go run ./cmd/trendpilot games                  # one Twitch + YouTube market scan
go run ./cmd/trendpilot trends -region US
go run ./cmd/trendpilot run                    # bot + scanners + local API
```

In a Codespace, add `YOUTUBE_API_KEY`, `TWITCH_CLIENT_ID` and `TWITCH_CLIENT_SECRET` as
[Codespaces secrets](https://github.com/settings/codespaces) for this repo; they arrive as
environment variables.

## How it works

```
Twitch top categories ─► games     viewers, channels, top-25 bar, genres (IGDB), growth/h
YouTube gaming chart ──┘    │      + hour-of-week history ─► /besttime
                            │
YouTube trending chart ─► trends    score = heat × (1 + growth) × (1 − crowding)
creator's draft ───────► rewrite    LLM: captions + hashtags, off-topic trends skipped
watched videos ────────► monitor    alerts on new comments / breakout, per Discord channel
                            │
          Discord slash commands · local JSON API (127.0.0.1:8790) · CLI

Creator's stream (TWITCH_CHANNEL)
  stream-status job (1/min) ─► go-live post (LLM, mentions stripped) · recap on 2 offline polls
  anonymous IRC chat ─► surge detector (20 s window vs EWMA, hype words weigh more)
        └─► clip (Helix, creator sign-in) + OBS SaveReplayBuffer + LLM title ─► Discord queue
            [Approve] [New title] [Discard]
Game scan job ─► category shifts (genre ±30%, game +50%/−40%, new entrants) ─► alerts [Mute 24h]
/raid ─► small live channels (3–30 viewers, live 20+ min) ─► [Raid] (creator sign-in, Manage Server)
```

**Hosted mode.** One instance serves many Discord servers. `live.Manager` keeps one
`Watcher` per server (from `/mychannel`), polls all of them in one batched
`StreamsByLogins` call per minute, and gives each a `PosterFor(guildID)` so posts never cross
servers. Per-server Twitch sign-ins are made from Discord with the device-code flow
(`/connect-twitch`) and are only accepted when the signed-in login matches `/mychannel`.
`/suggest` is rate-limited per person and per server; `llm.Client` allows 2 calls at once.
See [HOSTING.md](HOSTING.md).

Background work runs on `internal/jobs` (per-job goroutine, jitter, panic recovery);
`GET /api/jobs` shows each job's last run and error. Per-server channel choices live in
`settings.json`, the creator's Twitch token in `twitch-user.json` (0600), both in the
user config dir.

- **Opportunity** (games) = viewers ÷ (viewers of the #25 stream + 1) × growth boost. A big
  audience whose directory first screens a small stream can still reach.
- **Best go-live hour** uses the same ratio per hour of the week, a moving average with
  roughly 5 weeks of memory, saved to `history.json` in the user config dir.
- **Trend score** (hashtags): heat = Σ log(1 + views/hour); crowding = share of trending
  channels already using the term; single-channel tags are dropped as branding.

## Resource limits

`internal/usage` caps the process at 1 core (`GOMAXPROCS`), sets a 150 MB soft memory limit,
lowers OS priority (nice 10 / BELOW_NORMAL), and counts every HTTP byte. `GET /api/usage`
and the hourly `usage` log line report CPU %, memory and MB/hour.

## Layout

| Path | |
|---|---|
| `cmd/trendpilot` | entry point, setup wizard, env loading |
| `internal/games` | Twitch/YouTube game market scan, genres, history, best times |
| `internal/twitch` | Helix + IGDB client (app access token) |
| `internal/youtube` | YouTube Data API v3 client + link parser |
| `internal/trends` | hashtag/topic extraction and scoring |
| `internal/rewrite` | LLM prompt and parsing for `/suggest` |
| `internal/monitor` | per-owner watch list and alerts |
| `internal/llm` | OpenAI-compatible client (static key, or Hermes/Nous OAuth for local dev) |
| `internal/discord` | slash commands |
| `internal/server` | local JSON API |
| `internal/usage` | byte counting, CPU/memory report, priority and limits |
| `internal/cache` | generic TTL cache |
| `internal/jobs` | background job scheduler |
| `internal/live` | creator's stream: chat reader, surge detector, go-live/recap, highlight queue |
| `internal/obs` | OBS WebSocket v5 client (replay buffer) |
| `internal/store` | per-Discord-server settings (JSON) |

## Releasing

```bash
git tag v0.2.0 && git push origin v0.2.0
```

`.github/workflows/release.yml` tests, builds Windows/macOS/Linux archives (binary,
`SETUP.md`, a double-clickable *Change settings* launcher) and publishes a GitHub Release.

## Ground rules for contributions

TrendPilot informs creators; it never acts on platforms for them. PRs that add automated
posting of likes/comments/follows, view inflation, or proxying to disguise a creator's
location will be declined. They break platform rules and get creators' channels restricted.
