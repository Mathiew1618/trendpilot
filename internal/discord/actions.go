package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"trendpilot/internal/games"
	"trendpilot/internal/live"
	"trendpilot/internal/store"
)

// Settings commands and actions that affect the channel are limited to
// members with Manage Server.
var manageServer = int64(discordgo.PermissionManageServer)

const (
	whatAlerts  = "alerts"
	whatClips   = "clips"
	whatGoLive  = "golive"
	minRaidSize = 3
)

var whatChoices = []*discordgo.ApplicationCommandOptionChoice{
	{Name: "Trend alerts (genres and games spiking or dropping)", Value: whatAlerts},
	{Name: "Clips & highlights for approval", Value: whatClips},
	{Name: "Go-live announcements", Value: whatGoLive},
}

var actionCommands = []*discordgo.ApplicationCommand{
	{Name: "sendhere", Description: "Post TrendPilot updates in this channel", DefaultMemberPermissions: &manageServer,
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "what", Description: "Which updates", Required: true, Choices: whatChoices},
			{Type: discordgo.ApplicationCommandOptionRole, Name: "ping", Description: "Go-live only: a role to ping (leave empty for no ping)"},
		}},
	{Name: "stopsending", Description: "Stop posting a kind of update", DefaultMemberPermissions: &manageServer,
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "what", Description: "Which updates", Required: true, Choices: whatChoices},
		}},
	{Name: "raid", Description: "Small live channels in your game that a raid would really help", DefaultMemberPermissions: &manageServer,
		Options: []*discordgo.ApplicationCommandOption{
			str("game", "Game to search (default: what you're streaming now)", false),
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "max_viewers", Description: "Largest channel to suggest (default 30)"},
		}},
	{Name: "clip", Description: "Clip the last 30 seconds of your stream and queue it for approval", DefaultMemberPermissions: &manageServer},
}

func (b *Bot) canManage(i *discordgo.InteractionCreate) bool {
	return i.Member != nil && i.Member.Permissions&(discordgo.PermissionManageServer|discordgo.PermissionAdministrator) != 0
}

func (b *Bot) action(ctx context.Context, i *discordgo.InteractionCreate, name string, opt map[string]string) (string, []discordgo.MessageComponent) {
	if i.GuildID == "" {
		return "Use this inside your server, not in DMs.", nil
	}
	if !b.canManage(i) {
		return "Only members with **Manage Server** can do that.", nil
	}
	switch name {
	case "sendhere":
		return b.sendHere(i, opt["what"], opt["ping"]), nil
	case "stopsending":
		return b.stopSending(i, opt["what"]), nil
	case "raid":
		maxV, _ := strconv.Atoi(opt["max_viewers"])
		return b.raidCmd(ctx, opt["game"], maxV)
	case "clip":
		return b.clipCmd(ctx), nil
	}
	return "Unknown command.", nil
}

func (b *Bot) sendHere(i *discordgo.InteractionCreate, what, role string) string {
	if b.Store == nil {
		return "Settings storage is off."
	}
	err := b.Store.Update(i.GuildID, func(g *store.Guild) {
		switch what {
		case whatAlerts:
			g.AlertChannel = i.ChannelID
		case whatClips:
			g.QueueChannel = i.ChannelID
		case whatGoLive:
			g.AnnounceChannel, g.AnnounceMention = i.ChannelID, role
		}
	})
	if err != nil {
		return "Couldn't save that: " + err.Error()
	}
	switch what {
	case whatAlerts:
		if b.app.Games == nil {
			return "Saved, but trend alerts need Twitch keys (TWITCH_CLIENT_ID / SECRET) to work."
		}
		return "✅ Trend alerts will be posted here when a genre or game spikes or drops sharply. Each alert has a **Mute** button."
	case whatClips:
		if b.Watcher == nil {
			return "Saved, but clips need your channel name (TWITCH_CHANNEL) so TrendPilot can follow your chat. Run setup again to add it."
		}
		return "✅ When chat goes wild on your stream, the clip, a title and hashtags will land here for **Approve / New title / Discard**."
	default:
		if b.Watcher == nil {
			return "Saved, but go-live posts need your channel name (TWITCH_CHANNEL). Run setup again to add it."
		}
		ping := "no ping"
		if role != "" {
			ping = "pinging <@&" + role + ">"
		}
		return "✅ A fresh go-live announcement will be posted here each time you go live (" + ping + ")."
	}
}

func (b *Bot) stopSending(i *discordgo.InteractionCreate, what string) string {
	if b.Store == nil {
		return "Settings storage is off."
	}
	b.Store.Update(i.GuildID, func(g *store.Guild) {
		switch what {
		case whatAlerts:
			g.AlertChannel = ""
		case whatClips:
			g.QueueChannel = ""
		case whatGoLive:
			g.AnnounceChannel, g.AnnounceMention = "", ""
		}
	})
	return "Stopped."
}

func (b *Bot) raidCmd(ctx context.Context, game string, maxViewers int) (string, []discordgo.MessageComponent) {
	if b.app.Games == nil {
		return notReady, nil
	}
	if maxViewers <= 0 {
		maxViewers = 30
	}
	exclude := ""
	if b.Watcher != nil {
		exclude = b.Watcher.Channel
		if st, live := b.Watcher.Live(); live && game == "" {
			game = st.GameName
		}
	}
	if game == "" {
		return "Which game? Add `game:` (or go live first and I'll use your current game).", nil
	}
	targets, name, err := b.app.Games.RaidTargets(ctx, game, b.app.Games.Language, minRaidSize, maxViewers, exclude)
	if err != nil {
		return "Couldn't search Twitch: " + err.Error(), nil
	}
	if len(targets) == 0 {
		return fmt.Sprintf("No live %s channels with %d–%d viewers that have been on for 20+ minutes. Try a bigger `max_viewers`.", name, minRaidSize, maxViewers), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "**Raid targets in %s** (%d–%d viewers, live 20+ min)\n", name, minRaidSize, maxViewers)
	var raidBtns, linkBtns []discordgo.MessageComponent
	canRaid := b.User != nil && b.User.HasScope("channel:manage:raids")
	for n, t := range targets {
		mature := ""
		if t.Mature {
			mature = " · 🔞 mature"
		}
		fmt.Fprintf(&sb, "%d. **%s**: %d viewers, live %s%s\n   *%s*\n", n+1, t.Name, t.Viewers, t.Uptime, mature, clip(t.Title, 90))
		linkBtns = append(linkBtns, discordgo.Button{Label: clip(t.Name, 30), Style: discordgo.LinkButton, URL: "https://twitch.tv/" + t.Login})
		if canRaid {
			raidBtns = append(raidBtns, discordgo.Button{Label: "Raid " + clip(t.Name, 25), Style: discordgo.PrimaryButton,
				CustomID: "raid|" + t.UserID + "|" + clip(t.Name, 40)})
		}
	}
	if canRaid {
		sb.WriteString("\nPress **Raid** to send your viewers (you must be live). Twitch shows a 90-second countdown you can still cancel.")
	} else {
		sb.WriteString("\nType `/raid <name>` in your Twitch chat to raid. *(Sign in with `trendpilot twitch-login` to get 1-click Raid buttons here.)*")
	}
	comps := []discordgo.MessageComponent{discordgo.ActionsRow{Components: linkBtns}}
	if len(raidBtns) > 0 {
		comps = append([]discordgo.MessageComponent{discordgo.ActionsRow{Components: raidBtns}}, comps...)
	}
	return sb.String(), comps
}

func (b *Bot) clipCmd(ctx context.Context) string {
	if b.Watcher == nil {
		return "Clips need your channel name (TWITCH_CHANNEL). Run setup again to add it."
	}
	if _, live := b.Watcher.Live(); !live {
		return "You're not live right now."
	}
	go b.Watcher.HandleSurge(context.Background(), live.Surge{At: time.Now()})
	return "🎬 Clipping now. It'll appear in the clips channel in about 20 seconds."
}

// gamesButtons offers the next step for the best opening in /games.
func (b *Bot) gamesButtons(genre string) []discordgo.MessageComponent {
	if b.app.Games == nil {
		return nil
	}
	ops := b.app.Games.Opportunities(genre)
	if len(ops) == 0 {
		return nil
	}
	name := clip(ops[0].Name, 60)
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "Best time for " + clip(name, 50), Style: discordgo.SecondaryButton, CustomID: "bt|" + name},
		discordgo.Button{Label: "Raid targets in " + clip(name, 45), Style: discordgo.SecondaryButton, CustomID: "rt|" + name},
	}}}
}

// onComponent handles button presses. Custom IDs are "kind|arg|arg".
func (b *Bot) onComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.Split(i.MessageComponentData().CustomID, "|")
	reply := func(text string) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: clip(text, 1990), Flags: discordgo.MessageFlagsEphemeral}})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch parts[0] {
	case "bt":
		reply(b.bestTimeCmd(parts[1]))
	case "rt":
		if !b.canManage(i) {
			reply("Only members with **Manage Server** can do that.")
			return
		}
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral}})
		out, comps := b.raidCmd(ctx, parts[1], 0)
		edit := &discordgo.WebhookEdit{Content: &out}
		if len(comps) > 0 {
			edit.Components = &comps
		}
		s.InteractionResponseEdit(i.Interaction, edit)
	case "mute":
		if b.Store == nil || i.GuildID == "" || len(parts) < 2 {
			return
		}
		b.Store.Update(i.GuildID, func(g *store.Guild) {
			if g.Muted == nil {
				g.Muted = map[string]time.Time{}
			}
			g.Muted[parts[1]] = time.Now().Add(24 * time.Hour)
		})
		reply("🔕 Muted that alert for 24 hours.")
	case "raid":
		if !b.canManage(i) || b.User == nil || len(parts) < 3 {
			reply("Only members with **Manage Server** can start a raid, and the streamer must be signed in.")
			return
		}
		if err := b.User.StartRaid(ctx, parts[1]); err != nil {
			reply("Raid didn't start: " + err.Error())
			return
		}
		reply("🚀 Raid to **" + parts[2] + "** started. Twitch shows a countdown on your stream; you can still cancel it there.")
	case "q":
		b.onQueueButton(ctx, s, i, parts)
	}
}

func (b *Bot) onQueueButton(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, parts []string) {
	if len(parts) < 3 || b.Watcher == nil {
		return
	}
	who := "someone"
	if i.Member != nil && i.Member.User != nil {
		who = i.Member.User.Username
	}
	if !b.canManage(i) {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "Only members with **Manage Server** can review clips.", Flags: discordgo.MessageFlagsEphemeral}})
		return
	}
	expired := func() {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "This highlight is from before TrendPilot restarted, so its buttons no longer work. The clip link above still does.", Flags: discordgo.MessageFlagsEphemeral}})
	}
	var it live.QueueItem
	var ok bool
	switch parts[1] {
	case "ok":
		it, ok = b.Watcher.SetStatus(parts[2], "approved")
	case "del":
		it, ok = b.Watcher.SetStatus(parts[2], "discarded")
	case "new":
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
		if it, ok = b.Watcher.NewTitle(ctx, parts[2]); !ok {
			return
		}
		embeds, comps := queueMessage(it, "")
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &embeds, Components: &comps})
		return
	}
	if !ok {
		expired()
		return
	}
	embeds, comps := queueMessage(it, who)
	content := ""
	if it.Status == "approved" {
		// A plain block that copies cleanly into TikTok/Shorts/Reels.
		content = "```\n" + it.Title + "\n" + strings.Join(it.Hashtags, " ") + "\n```"
	}
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Content: content, Embeds: embeds, Components: comps}})
}

func queueMessage(it live.QueueItem, by string) ([]*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	e := &discordgo.MessageEmbed{
		Title:       clip(it.Title, 250),
		Description: strings.Join(it.Hashtags, " "),
		Color:       0x8B5CF6,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "When", Value: "at " + offset(it.Offset) + " into the stream", Inline: true},
			{Name: "Game", Value: or(it.Game, "—"), Inline: true},
		},
	}
	switch {
	case it.ClipURL != "":
		e.URL = it.ClipURL
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Clip", Value: it.ClipURL})
	case it.ClipNote != "":
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Clip", Value: clip(it.ClipNote, 200)})
	}
	if it.ReplaySaved {
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "OBS", Value: "Replay buffer saved to your recordings folder.", Inline: true})
	}
	if len(it.Chat) > 0 {
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Chat at that moment", Value: "```\n" + clip(strings.Join(it.Chat, "\n"), 900) + "\n```"})
	}

	var comps []discordgo.MessageComponent
	links := []discordgo.MessageComponent{}
	if it.EditURL != "" {
		links = append(links, discordgo.Button{Label: "Edit clip on Twitch", Style: discordgo.LinkButton, URL: it.EditURL})
	}
	switch it.Status {
	case "approved":
		e.Color = 0x22C55E
		e.Footer = &discordgo.MessageEmbedFooter{Text: "✅ Approved by " + by + ". Copy the title and hashtags above."}
	case "discarded":
		e.Color = 0x6B7280
		e.Footer = &discordgo.MessageEmbedFooter{Text: "Discarded by " + by}
		links = nil
	default:
		e.Footer = &discordgo.MessageEmbedFooter{Text: "Waiting for approval"}
		comps = append(comps, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Approve", Style: discordgo.SuccessButton, CustomID: "q|ok|" + it.ID},
			discordgo.Button{Label: "New title", Style: discordgo.PrimaryButton, CustomID: "q|new|" + it.ID},
			discordgo.Button{Label: "Discard", Style: discordgo.DangerButton, CustomID: "q|del|" + it.ID},
		}})
	}
	if len(links) > 0 {
		comps = append(comps, discordgo.ActionsRow{Components: links})
	}
	return []*discordgo.MessageEmbed{e}, comps
}

// --- live.Poster: posts that come from TrendPilot, not from a command ---

func (b *Bot) eachGuild(fn func(guildID string, g store.Guild)) {
	if b.Store != nil {
		b.Store.Each(fn)
	}
}

func (b *Bot) GoLive(ctx context.Context, text string) {
	b.eachGuild(func(_ string, g store.Guild) {
		if g.AnnounceChannel == "" {
			return
		}
		msg := &discordgo.MessageSend{Content: text, AllowedMentions: &discordgo.MessageAllowedMentions{}}
		if g.AnnounceMention != "" {
			msg.Content = "<@&" + g.AnnounceMention + "> " + text
			msg.AllowedMentions.Roles = []string{g.AnnounceMention}
		}
		if _, err := b.s.ChannelMessageSendComplex(g.AnnounceChannel, msg); err != nil {
			slog.Warn("go-live post failed", "channel", g.AnnounceChannel, "err", err)
		}
	})
}

func (b *Bot) Queue(ctx context.Context, it *live.QueueItem) {
	embeds, comps := queueMessage(*it, "")
	b.eachGuild(func(_ string, g store.Guild) {
		if g.QueueChannel == "" {
			return
		}
		if _, err := b.s.ChannelMessageSendComplex(g.QueueChannel, &discordgo.MessageSend{
			Content: "🎬 **Chat just went wild.** New highlight to review:", Embeds: embeds, Components: comps,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}); err != nil {
			slog.Warn("clip queue post failed", "channel", g.QueueChannel, "err", err)
		}
	})
}

func (b *Bot) Recap(ctx context.Context, text string) {
	b.eachGuild(func(_ string, g store.Guild) {
		if g.QueueChannel == "" {
			return
		}
		b.s.ChannelMessageSendComplex(g.QueueChannel, &discordgo.MessageSend{Content: clip(text, 1990), AllowedMentions: &discordgo.MessageAllowedMentions{}})
	})
}

// PostShifts sends category shift alerts to every server that asked for
// them, skipping muted ones.
func (b *Bot) PostShifts(shifts []games.Shift) {
	if len(shifts) == 0 {
		return
	}
	now := time.Now()
	b.eachGuild(func(_ string, g store.Guild) {
		if g.AlertChannel == "" {
			return
		}
		var lines []string
		var btns []discordgo.MessageComponent
		for _, sh := range shifts {
			if g.IsMuted(sh.Key, now) || len(lines) == 3 {
				continue
			}
			icon := "📈"
			if sh.Change < 0 {
				icon = "📉"
			}
			lines = append(lines, icon+" "+sh.String())
			btns = append(btns, discordgo.Button{Label: "Mute " + clip(sh.Name, 40) + " 24h", Style: discordgo.SecondaryButton, CustomID: "mute|" + sh.Key})
		}
		if len(lines) == 0 {
			return
		}
		msg := &discordgo.MessageSend{
			Content:         "**Twitch category shift**\n" + strings.Join(lines, "\n"),
			Components:      []discordgo.MessageComponent{discordgo.ActionsRow{Components: btns}},
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}
		if _, err := b.s.ChannelMessageSendComplex(g.AlertChannel, msg); err != nil {
			slog.Warn("shift alert failed", "channel", g.AlertChannel, "err", err)
		}
	})
}

func offset(d time.Duration) string {
	d = d.Round(time.Second)
	return fmt.Sprintf("%d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
