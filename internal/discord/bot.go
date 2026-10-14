// Package discord exposes TrendPilot as Discord slash commands. Monitor
// alerts are posted to the channel where /watch was run.
package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"trendpilot/internal/app"
	"trendpilot/internal/live"
	"trendpilot/internal/monitor"
	"trendpilot/internal/rewrite"
	"trendpilot/internal/store"
	"trendpilot/internal/twitch"
	"trendpilot/internal/youtube"
)

type Bot struct {
	s     *discordgo.Session
	app   *app.App
	guild string // "" registers commands globally (can take up to an hour to appear)

	// Optional, set by main before Run.
	Store   *store.Store     // per-server channel settings
	Watcher *live.Watcher    // the creator's own stream (TWITCH_CHANNEL)
	User    *twitch.UserAuth // the creator's Twitch sign-in (clips, raids)
}

func New(token, guildID string, a *app.App) (*Bot, error) {
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	s.Identify.Intents = discordgo.IntentsGuilds
	b := &Bot{s: s, app: a, guild: guildID}
	s.AddHandler(b.onInteraction)
	return b, nil
}

// Notify is a monitor.Notifier: owner is the Discord channel ID.
func (b *Bot) Notify(owner string, a monitor.Alert) {
	if owner == "" {
		return
	}
	icon := "💬"
	if a.Kind == "breakout" {
		icon = "🚀"
	}
	msg := fmt.Sprintf("%s %s\nhttps://youtu.be/%s", icon, a.Message, a.VideoID)
	if _, err := b.s.ChannelMessageSend(owner, msg); err != nil {
		slog.Warn("discord alert failed", "channel", owner, "err", err)
	}
}

func str(name, desc string, required bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionString, Name: name, Description: desc, Required: required}
}

var commands = []*discordgo.ApplicationCommand{
	{Name: "trends", Description: "Rising hashtags and topics right now", Options: []*discordgo.ApplicationCommandOption{
		str("region", "Two-letter country code, e.g. US, GB, BR", false),
		str("category", "YouTube category ID, e.g. 20 = Gaming, 10 = Music", false),
	}},
	{Name: "suggest", Description: "Trend-aware caption and hashtag ideas for your draft", Options: []*discordgo.ApplicationCommandOption{
		str("text", "Your draft title or caption", true),
		{Type: discordgo.ApplicationCommandOptionString, Name: "platform", Description: "Where you'll post it", Choices: []*discordgo.ApplicationCommandOptionChoice{
			{Name: "YouTube", Value: "youtube"}, {Name: "TikTok", Value: "tiktok"}, {Name: "Instagram", Value: "instagram"}, {Name: "Twitch", Value: "twitch"},
		}},
		str("niche", "Your niche, e.g. cozy gaming, street food", false),
		str("region", "Two-letter country code", false),
	}},
	{Name: "watch", Description: "Track a YouTube video and get alerts in this channel", Options: []*discordgo.ApplicationCommandOption{
		str("video", "YouTube link or video ID", true),
	}},
	{Name: "unwatch", Description: "Stop tracking a video", Options: []*discordgo.ApplicationCommandOption{
		str("video", "YouTube link or video ID", true),
	}},
	{Name: "watching", Description: "Videos tracked from this channel"},
}

// Run connects, registers commands and blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	if err := b.s.Open(); err != nil {
		return fmt.Errorf("discord connect: %w", err)
	}
	defer b.s.Close()
	if _, err := b.s.ApplicationCommandBulkOverwrite(b.s.State.User.ID, b.guild, allCommands()); err != nil {
		return fmt.Errorf("discord register commands: %w", err)
	}
	slog.Info("discord bot online", "user", b.s.State.User.Username, "guild", b.guild)
	<-ctx.Done()
	return nil
}

func allCommands() []*discordgo.ApplicationCommand {
	all := append([]*discordgo.ApplicationCommand{}, commands...)
	all = append(all, gameCommands...)
	return append(all, actionCommands...)
}

func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type == discordgo.InteractionMessageComponent {
		b.onComponent(s, i)
		return
	}
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data := i.ApplicationCommandData()
	opt := map[string]string{}
	for _, o := range data.Options {
		opt[o.Name] = fmt.Sprint(o.Value) // StringValue panics on number options
	}

	// Trend fetches and LLM calls can exceed Discord's 3 s reply window.
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var out string
	var comps []discordgo.MessageComponent
	switch data.Name {
	case "trends":
		out = b.trends(ctx, strings.ToUpper(opt["region"]), opt["category"])
	case "suggest":
		out = b.suggest(ctx, opt)
	case "watch":
		out = b.watch(opt["video"], i.ChannelID)
	case "unwatch":
		out = b.unwatch(opt["video"], i.ChannelID)
	case "watching":
		out = b.watching(i.ChannelID)
	case "games":
		out = b.gamesCmd(opt["genre"])
		comps = b.gamesButtons(opt["genre"])
	case "sendhere", "stopsending", "raid", "clip":
		out, comps = b.action(ctx, i, data.Name, opt)
	case "genres":
		out = b.genresCmd()
	case "besttime":
		out = b.bestTimeCmd(opt["game"])
	case "viewers":
		out = b.viewersCmd()
	default:
		out = "Unknown command."
	}
	out = clip(out, 1990)
	edit := &discordgo.WebhookEdit{Content: &out}
	if len(comps) > 0 {
		edit.Components = &comps
	}
	if _, err := s.InteractionResponseEdit(i.Interaction, edit); err != nil {
		slog.Warn("discord reply failed", "cmd", data.Name, "err", err)
	}
}

func (b *Bot) trends(ctx context.Context, region, category string) string {
	ts, err := b.app.Trends(ctx, region, category)
	if err != nil {
		return "Couldn't fetch trends: " + err.Error()
	}
	if len(ts) == 0 {
		return "No cross-channel trends in that chart right now."
	}
	if region == "" {
		region = b.app.Region
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Trending opportunities — %s** (rising + less crowded first)\n", region)
	for n, t := range ts[:min(len(ts), 12)] {
		arrow := "→"
		if t.Growth > 0.05 {
			arrow = "↑"
		} else if t.Growth < -0.05 {
			arrow = "↓"
		}
		fmt.Fprintf(&sb, "%d. **%s** %s score %.1f · %d channels · e.g. *%s*\n", n+1, t.Term, arrow, t.Score, t.Channels, clip(first(t.Examples), 60))
	}
	return sb.String()
}

func (b *Bot) suggest(ctx context.Context, opt map[string]string) string {
	s, err := b.app.Suggest(ctx, rewrite.Draft{Text: opt["text"], Platform: opt["platform"], Niche: opt["niche"]}, strings.ToUpper(opt["region"]))
	if err != nil {
		return "Couldn't build suggestions: " + err.Error()
	}
	var sb strings.Builder
	sb.WriteString("**Caption ideas**\n")
	for n, c := range s.Captions {
		fmt.Fprintf(&sb, "%d. %s\n   *%s*\n", n+1, c.Text, c.Reason)
	}
	sb.WriteString("\n**Hashtags**\n")
	var tags []string
	for _, h := range s.Hashtags {
		mark := ""
		if h.Trending {
			mark = " 📈"
		}
		tags = append(tags, h.Tag+mark)
	}
	sb.WriteString(strings.Join(tags, "  ") + "\n")
	if len(s.Skipped) > 0 {
		sb.WriteString("\n*Trending but off-topic for this post (skipped):* " + strings.Join(s.Skipped, " "))
	}
	return sb.String()
}

func (b *Bot) watch(video, channel string) string {
	id, ok := youtube.VideoID(video)
	if !ok {
		return "That doesn't look like a YouTube link or video ID."
	}
	if err := b.app.Monitor.Watch(id, channel); err != nil {
		return err.Error()
	}
	return fmt.Sprintf("Watching https://youtu.be/%s. I'll post here when new comments arrive or it starts breaking out.", id)
}

func (b *Bot) unwatch(video, channel string) string {
	id, ok := youtube.VideoID(video)
	if !ok || !b.app.Monitor.Unwatch(id, channel) {
		return "I wasn't watching that video."
	}
	return "Stopped watching " + id + "."
}

func (b *Bot) watching(channel string) string {
	var sb strings.Builder
	for _, st := range b.app.Monitor.List() {
		if st.Owner != channel {
			continue
		}
		title := st.Title
		if title == "" {
			title = st.VideoID + " (first check pending)"
		}
		fmt.Fprintf(&sb, "• **%s** — %d views, %.0f/h, %.1f%% engagement\n", clip(title, 60), st.Latest.Views, st.ViewsPerHour, st.Engagement*100)
	}
	if sb.Len() == 0 {
		return "Nothing watched from this channel. Use /watch with a YouTube link."
	}
	return sb.String()
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func first(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return xs[0]
}
