package discord

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"trendpilot/internal/games"
)

var gameCommands = []*discordgo.ApplicationCommand{
	{Name: "games", Description: "Games trending on Twitch and YouTube right now, and where a creator can stand out", Options: []*discordgo.ApplicationCommandOption{
		str("genre", "Only this genre, e.g. Shooter, RPG, Strategy", false),
	}},
	{Name: "genres", Description: "Which game genres are drawing viewers right now"},
	{Name: "besttime", Description: "Best hours to go live or post, learned from viewer patterns", Options: []*discordgo.ApplicationCommandOption{
		str("game", "A game name; empty for the whole market", false),
	}},
	{Name: "viewers", Description: "Overall viewer trend: up or down, what's rising and falling"},
}

const notReady = "Twitch isn't set up on this bot (TWITCH_CLIENT_ID / TWITCH_CLIENT_SECRET), so game trends are off."

func (b *Bot) scanned() (games.Snapshot, string) {
	if b.app.Games == nil {
		return games.Snapshot{}, notReady
	}
	s, ok := b.app.Games.Latest()
	if !ok {
		return s, "First scan of Twitch is still running. Try again in a minute."
	}
	return s, ""
}

func (b *Bot) gamesCmd(genre string) string {
	snap, msg := b.scanned()
	if msg != "" {
		return msg
	}
	var sb strings.Builder
	lang := ""
	if snap.Language != "" {
		lang = " · " + snap.Language + " streams"
	}
	fmt.Fprintf(&sb, "**Trending on Twitch now**%s (%s viewers in the top %d categories)\n", lang, num(snap.Viewers), len(snap.Games))
	n := 0
	for _, g := range snap.Games {
		if genre != "" && !matches(g, genre) {
			continue
		}
		if n++; n > 8 {
			break
		}
		fmt.Fprintf(&sb, "%d. **%s** %s %s viewers · %s channels · %s%s\n", n, g.Name, arrow(g.Growth), num(g.Viewers), channels(g), strings.Join(g.Genres, "/"), yt(g))
	}
	if n == 0 {
		return "No trending games in that genre right now. Try /genres to see what's live."
	}

	sb.WriteString("\n**Best openings for a growing channel** (big audience, low bar to reach the first screens)\n")
	ops := b.app.Games.Opportunities(genre)
	for i, g := range ops[:min(5, len(ops))] {
		bar := fmt.Sprintf("~%s viewers gets you top 25", num(g.EntryBar))
		if g.EntryBar == 0 {
			bar = "fewer than 25 live channels"
		}
		fmt.Fprintf(&sb, "%d. **%s**: %s viewers, %s%s\n", i+1, g.Name, num(g.Viewers), bar, growthNote(g.Growth))
	}
	fmt.Fprintf(&sb, "\n*Scanned %s.*", ago(snap.At))
	return sb.String()
}

func (b *Bot) genresCmd() string {
	snap, msg := b.scanned()
	if msg != "" {
		return msg
	}
	var sb strings.Builder
	sb.WriteString("**Genres by live viewers** (a game with several genres counts toward each)\n")
	gs := games.Genres(snap)
	for i, g := range gs[:min(10, len(gs))] {
		share := 100 * float64(g.Viewers) / float64(max(snap.Viewers, 1))
		fmt.Fprintf(&sb, "%d. **%s** %s %s viewers (%.0f%%) · %s\n", i+1, g.Name, arrow(g.Growth), num(g.Viewers), share, strings.Join(g.Games, ", "))
	}
	return sb.String()
}

func (b *Bot) bestTimeCmd(game string) string {
	if b.app.Games == nil {
		return notReady
	}
	key, name, ok := b.app.Games.Find(game)
	if !ok {
		return fmt.Sprintf("I haven't seen %q in the top Twitch categories yet. Pick a game from /games.", game)
	}
	loc := b.app.Games.Loc
	bt, ok := b.app.Games.Hist.Best(key, loc, time.Now())
	if !ok || bt.Coverage == 0 {
		return "No viewer history yet. Best times appear after the bot has run for a few hours, and get sharper over a week."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Best times: %s** (%s; %d of 168 weekly hours sampled)\n", name, loc, bt.Coverage)
	if len(bt.NextDay) > 0 {
		sb.WriteString("\n**Next 24 h, best hours to go live:**\n")
		for _, s := range bt.NextDay {
			fmt.Fprintf(&sb, "• %s: ~%s viewers, top-25 bar ~%s\n", when(s), num(int(s.Viewers)), num(int(s.EntryBar)))
		}
	}
	if len(bt.GoLive) > 0 {
		sb.WriteString("\n**Best go-live hours this week** (most viewers per unit of competition):\n")
		for _, s := range bt.GoLive {
			fmt.Fprintf(&sb, "• %s: ~%s viewers, top-25 bar ~%s\n", when(s), num(int(s.Viewers)), num(int(s.EntryBar)))
		}
	}
	sb.WriteString("\n**Peak audience hours:**\n")
	for _, s := range bt.PeakWeek[:min(3, len(bt.PeakWeek))] {
		fmt.Fprintf(&sb, "• %s: ~%s viewers\n", when(s), num(int(s.Viewers)))
	}
	if len(bt.YTPublish) > 0 {
		var hs []string
		for _, h := range bt.YTPublish {
			hs = append(hs, hour(h))
		}
		fmt.Fprintf(&sb, "\n**YouTube:** trending gaming videos were mostly published around %s. Upload an hour or two before that so the video has views when the audience arrives.\n", strings.Join(hs, ", "))
	}
	if bt.Coverage < 168 {
		sb.WriteString("\n*Still learning. Hours not sampled yet are missing from these lists.*")
	}
	return sb.String()
}

func (b *Bot) viewersCmd() string {
	if b.app.Games == nil {
		return notReady
	}
	o, ok := b.app.Games.Overview()
	if !ok {
		return "First scan of Twitch is still running. Try again in a minute."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Twitch right now:** %s viewers in the top categories", num(o.Viewers))
	if o.HourAgo > 0 {
		fmt.Fprintf(&sb, " (%s vs an hour ago", pct(o.Viewers, o.HourAgo))
		if o.DayAgo > 0 {
			fmt.Fprintf(&sb, ", %s vs this time yesterday", pct(o.Viewers, o.DayAgo))
		}
		sb.WriteString(")")
	}
	sb.WriteString("\n")
	if len(o.Rising) > 0 {
		sb.WriteString("\n📈 **Rising:** ")
		var xs []string
		for _, g := range o.Rising {
			xs = append(xs, fmt.Sprintf("%s (+%.0f%%)", g.Name, g.Growth*100))
		}
		sb.WriteString(strings.Join(xs, ", ") + "\n")
	}
	if len(o.Falling) > 0 {
		sb.WriteString("📉 **Falling:** ")
		var xs []string
		for _, g := range o.Falling {
			xs = append(xs, fmt.Sprintf("%s (%.0f%%)", g.Name, g.Growth*100))
		}
		sb.WriteString(strings.Join(xs, ", ") + "\n")
	}
	if o.HourAgo == 0 {
		sb.WriteString("\n*Rising and falling need an hour of scans.*\n")
	}
	sb.WriteString("\n**Top genres:** ")
	var xs []string
	for _, g := range o.TopGenres {
		xs = append(xs, fmt.Sprintf("%s %s", g.Name, num(g.Viewers)))
	}
	sb.WriteString(strings.Join(xs, " · "))
	return sb.String()
}

func matches(g games.GameStat, genre string) bool {
	for _, x := range g.Genres {
		if strings.Contains(strings.ToLower(x), strings.ToLower(genre)) {
			return true
		}
	}
	return false
}

func arrow(growth float64) string {
	switch {
	case growth > 0.05:
		return "↑"
	case growth < -0.05:
		return "↓"
	}
	return "→"
}

func growthNote(g float64) string {
	if g > 0.05 {
		return fmt.Sprintf(", rising %.0f%%/h", g*100)
	}
	return ""
}

func channels(g games.GameStat) string {
	if g.More {
		return num(g.Channels) + "+"
	}
	return num(g.Channels)
}

func yt(g games.GameStat) string {
	if g.YTVideos == 0 {
		return ""
	}
	return fmt.Sprintf(" · ▶️ %d on YouTube trending", g.YTVideos)
}

func num(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func pct(now, before int) string {
	return fmt.Sprintf("%+.0f%%", 100*float64(now-before)/float64(before))
}

func hour(h int) string {
	return time.Date(2000, 1, 1, h, 0, 0, 0, time.UTC).Format("3 PM")
}

func when(s games.Slot) string { return s.Weekday.String()[:3] + " " + hour(s.Hour) }

func ago(t time.Time) string {
	d := time.Since(t).Round(time.Minute)
	if d < time.Minute {
		return "just now"
	}
	return d.String() + " ago"
}
