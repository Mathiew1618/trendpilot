package live

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"trendpilot/internal/trends"
	"trendpilot/internal/twitch"
)

// QueueItem is one highlight awaiting the creator's approval in Discord.
type QueueItem struct {
	ID          string        `json:"id"`
	At          time.Time     `json:"at"`
	Offset      time.Duration `json:"offset"` // into the stream
	Game        string        `json:"game"`
	StreamTitle string        `json:"stream_title"`
	ClipURL     string        `json:"clip_url,omitempty"`
	EditURL     string        `json:"edit_url,omitempty"`
	ClipNote    string        `json:"clip_note,omitempty"` // why there is no clip
	ReplaySaved bool          `json:"replay_saved"`
	Title       string        `json:"title"`
	Hashtags    []string      `json:"hashtags"`
	Chat        []string      `json:"chat"`   // a few messages from the moment
	Status      string        `json:"status"` // pending, approved, discarded
	Platform    string        `json:"platform"`
}

type Poster interface {
	GoLive(ctx context.Context, text string)
	Queue(ctx context.Context, it *QueueItem)
	Recap(ctx context.Context, text string)
}

type Writer interface {
	Chat(ctx context.Context, system, user string) (string, error)
}

type StreamSource interface {
	StreamByLogin(ctx context.Context, login string) (twitch.Stream, bool, error)
}

type Clipper interface {
	CreateClip(ctx context.Context) (twitch.Clip, error)
}

type Replayer interface {
	SaveReplay(ctx context.Context) (bool, error)
}

type Watcher struct {
	Channel string
	src     StreamSource
	poster  Poster
	writer  Writer   // may be nil: templates are used instead
	clipper Clipper  // nil when the creator hasn't signed in
	replay  Replayer // nil when OBS isn't configured
	trends  func() []trends.Trend
	now     func() time.Time

	mu       sync.Mutex
	live     bool
	offline  int // consecutive offline polls
	stream   twitch.Stream
	peak     int
	items    map[string]*QueueItem
	order    []string
	session  []*QueueItem // highlights from the current stream
	detector *SurgeDetector
	busy     bool // a surge is being processed
	seq      int
}

func NewWatcher(channel string, src StreamSource, poster Poster, writer Writer, clipper Clipper, replay Replayer, trendsFn func() []trends.Trend) *Watcher {
	if trendsFn == nil {
		trendsFn = func() []trends.Trend { return nil }
	}
	return &Watcher{Channel: strings.ToLower(channel), src: src, poster: poster, writer: writer,
		clipper: clipper, replay: replay, trends: trendsFn, now: time.Now,
		items: map[string]*QueueItem{}, detector: NewSurgeDetector()}
}

func (w *Watcher) Live() (twitch.Stream, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stream, w.live
}

// Poll checks whether the channel is live; run it about once a minute.
func (w *Watcher) Poll(ctx context.Context) error {
	st, online, err := w.src.StreamByLogin(ctx, w.Channel)
	if err != nil {
		return err
	}
	w.mu.Lock()
	switch {
	case online && !w.live:
		w.live, w.offline, w.stream, w.peak = true, 0, st, st.ViewerCount
		w.session, w.detector = nil, NewSurgeDetector()
		w.mu.Unlock()
		w.poster.GoLive(ctx, w.hype(ctx, st))
		return nil
	case online:
		w.offline, w.stream = 0, st
		w.peak = max(w.peak, st.ViewerCount)
	case w.live:
		// Twitch's API can blink offline for a poll; wait for two in a row.
		if w.offline++; w.offline >= 2 {
			w.live = false
			recap := w.recapLocked()
			w.mu.Unlock()
			w.poster.Recap(ctx, recap)
			return nil
		}
	}
	w.mu.Unlock()
	return nil
}

// RunChat reads the channel's chat and processes surges while live.
func (w *Watcher) RunChat(ctx context.Context) {
	msgs := make(chan ChatMessage, 256)
	go ReadChat(ctx, w.Channel, msgs)
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-msgs:
			w.mu.Lock()
			if !w.live {
				w.mu.Unlock()
				continue
			}
			s, surged := w.detector.Add(m)
			start := surged && !w.busy
			if start {
				w.busy = true
			}
			w.mu.Unlock()
			if start {
				go func() {
					defer func() { w.mu.Lock(); w.busy = false; w.mu.Unlock() }()
					w.HandleSurge(ctx, s)
				}()
			}
		}
	}
}

// HandleSurge clips the moment, saves the OBS replay buffer, drafts a
// title and hashtags, and sends it all to the Discord queue.
func (w *Watcher) HandleSurge(ctx context.Context, s Surge) {
	w.mu.Lock()
	w.seq++
	it := &QueueItem{
		ID: strconv.FormatInt(s.At.Unix(), 36) + strconv.Itoa(w.seq), At: s.At,
		Offset: s.At.Sub(w.stream.StartedAt).Round(time.Second),
		Game:   w.stream.GameName, StreamTitle: w.stream.Title, Status: "pending", Platform: "tiktok",
	}
	w.mu.Unlock()
	for _, m := range s.Sample[max(0, len(s.Sample)-8):] {
		it.Chat = append(it.Chat, m.User+": "+m.Text)
	}

	var wg sync.WaitGroup
	if w.clipper != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()
			clip, err := w.clipper.CreateClip(cctx)
			it.ClipURL, it.EditURL = clip.URL, clip.EditURL
			if it.ClipURL == "" && clip.EditURL != "" {
				it.ClipURL = clip.EditURL
			}
			if err != nil {
				it.ClipNote = err.Error()
			}
		}()
	} else {
		it.ClipNote = "Sign in with `trendpilot twitch-login` to get automatic clips."
	}
	if w.replay != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := w.replay.SaveReplay(ctx)
			it.ReplaySaved = ok
			if err != nil {
				slog.Warn("OBS replay save failed", "err", err)
			}
		}()
	}
	wg.Wait()
	w.Retitle(ctx, it)

	w.mu.Lock()
	w.items[it.ID] = it
	w.order = append(w.order, it.ID)
	if len(w.order) > 100 {
		delete(w.items, w.order[0])
		w.order = w.order[1:]
	}
	w.session = append(w.session, it)
	cp := *it
	w.mu.Unlock()
	w.poster.Queue(ctx, &cp)
}

// Item returns a copy of a queued highlight, for Discord button presses.
func (w *Watcher) Item(id string) (QueueItem, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	it, ok := w.items[id]
	if !ok {
		return QueueItem{}, false
	}
	return *it, true
}

// SetStatus marks an item approved or discarded and returns the result.
func (w *Watcher) SetStatus(id, status string) (QueueItem, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	it, ok := w.items[id]
	if !ok {
		return QueueItem{}, false
	}
	it.Status = status
	return *it, true
}

// NewTitle drafts a fresh title and hashtags for an item.
func (w *Watcher) NewTitle(ctx context.Context, id string) (QueueItem, bool) {
	cp, ok := w.Item(id)
	if !ok {
		return cp, false
	}
	w.Retitle(ctx, &cp) // slow LLM call, outside the lock
	w.mu.Lock()
	defer w.mu.Unlock()
	it, ok := w.items[id]
	if !ok {
		return cp, false
	}
	it.Title, it.Hashtags = cp.Title, cp.Hashtags
	return *it, true
}

const titleSystem = `You title short clips cut from a live stream, for TikTok, YouTube Shorts and Reels.
Use the chat reaction to guess what happened. Be specific and curious, not clickbait, 80 characters max.
Pick 3-5 hashtags that fit the game and the moment; prefer listed trending tags only if they fit.
Reply with JSON only: {"title":"...","hashtags":["#..."]}`

// Retitle (re)drafts an item's title and hashtags.
func (w *Watcher) Retitle(ctx context.Context, it *QueueItem) {
	fallback := func() {
		it.Title = fmt.Sprintf("Chat lost it at %s in %s", fmtOffset(it.Offset), or(it.Game, "the stream"))
		it.Hashtags = []string{"#" + camel(or(it.Game, "Gaming")), "#Twitch", "#Gaming"}
	}
	if w.writer == nil {
		fallback()
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Game: %s\nStream title: %s\nPrevious title (write a different one): %s\nChat at the moment:\n", it.Game, it.StreamTitle, it.Title)
	for _, c := range it.Chat {
		b.WriteString("  " + c + "\n")
	}
	if ts := w.trends(); len(ts) > 0 {
		b.WriteString("Trending tags:")
		for _, t := range ts[:min(10, len(ts))] {
			b.WriteString(" " + t.Term)
		}
		b.WriteString("\n")
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := w.writer.Chat(cctx, titleSystem, b.String())
	var r struct {
		Title    string   `json:"title"`
		Hashtags []string `json:"hashtags"`
	}
	if err == nil {
		if i, j := strings.Index(out, "{"), strings.LastIndex(out, "}"); i >= 0 && j > i {
			err = json.Unmarshal([]byte(out[i:j+1]), &r)
		}
	}
	if err != nil || strings.TrimSpace(r.Title) == "" {
		if err != nil {
			slog.Warn("clip title draft failed", "err", err)
		}
		fallback()
		return
	}
	it.Title = clean(r.Title, 100)
	it.Hashtags = nil
	for _, h := range r.Hashtags[:min(6, len(r.Hashtags))] {
		h = "#" + strings.TrimLeft(strings.ReplaceAll(strings.TrimSpace(h), " ", ""), "#")
		if len(h) > 1 {
			it.Hashtags = append(it.Hashtags, h)
		}
	}
}

const hypeSystem = `Write a go-live announcement for a streamer's Discord server.
1-3 short sentences, upbeat and natural, in the streamer's voice. At most two emoji.
Mention what they're playing and tease the stream title. Reference a trend only if it truly fits.
Never write @everyone, @here, user mentions or links; those are added separately. Plain text only.`

func (w *Watcher) hype(ctx context.Context, st twitch.Stream) string {
	link := "https://twitch.tv/" + w.Channel
	fallback := fmt.Sprintf("🔴 **%s** is live: %s\n%s", or(st.UserName, w.Channel), or(st.Title, st.GameName), link)
	if w.writer == nil {
		return fallback
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Streamer: %s\nGame: %s\nStream title: %s\n", or(st.UserName, w.Channel), st.GameName, st.Title)
	if ts := w.trends(); len(ts) > 0 {
		b.WriteString("Trending now:")
		for _, t := range ts[:min(8, len(ts))] {
			b.WriteString(" " + t.Term)
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := w.writer.Chat(cctx, hypeSystem, b.String())
	if err != nil || strings.TrimSpace(out) == "" {
		if err != nil {
			slog.Warn("go-live text failed; using template", "err", err)
		}
		return fallback
	}
	return "🔴 " + clean(out, 500) + "\n" + link
}

func (w *Watcher) recapLocked() string {
	var b strings.Builder
	dur := w.now().Sub(w.stream.StartedAt).Round(time.Minute)
	fmt.Fprintf(&b, "**Stream ended.** %s of %s, peak **%d** viewers.\n", fmtDur(dur), or(w.stream.GameName, "streaming"), w.peak)
	if len(w.session) == 0 {
		b.WriteString("No chat surges this time.")
		return b.String()
	}
	fmt.Fprintf(&b, "%d highlight(s):\n", len(w.session))
	for _, it := range w.session {
		link := it.ClipURL
		if link == "" {
			link = "(no clip)"
		}
		fmt.Fprintf(&b, "• %s %s · %s\n", fmtOffset(it.Offset), it.Title, link)
	}
	return b.String()
}

var mentionRE = regexp.MustCompile(`@(everyone|here)|<@[!&]?\d+>|<#\d+>|https?://\S+`)

// clean strips anything that could ping people or smuggle links from model
// output, and caps its length.
func clean(s string, n int) string {
	s = strings.TrimSpace(mentionRE.ReplaceAllString(s, ""))
	s = strings.Trim(s, `"`)
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}

func camel(s string) string {
	var b strings.Builder
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') }) {
		b.WriteString(strings.ToUpper(f[:1]) + f[1:])
	}
	return b.String()
}

func fmtOffset(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	return fmt.Sprintf("%d:%02d:%02d", h, m, s)
}

func fmtDur(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
