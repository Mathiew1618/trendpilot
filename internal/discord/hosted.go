package discord

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"trendpilot/internal/live"
	"trendpilot/internal/store"
	"trendpilot/internal/twitch"
)

// Per-server creator features, so one hosted TrendPilot can serve many
// Discord servers: each sets its own channel, time zone and Twitch sign-in.

var hostedCommands = []*discordgo.ApplicationCommand{
	{Name: "mychannel", Description: "Set your Twitch channel: go-live posts, chat highlights, raid suggestions", DefaultMemberPermissions: &manageServer,
		Options: []*discordgo.ApplicationCommandOption{str("channel", "Your Twitch channel name (the part after twitch.tv/)", true)}},
	{Name: "connect-twitch", Description: "Sign in to Twitch so TrendPilot can clip your stream and raid with one click", DefaultMemberPermissions: &manageServer},
	{Name: "disconnect-twitch", Description: "Remove TrendPilot's Twitch sign-in for this server", DefaultMemberPermissions: &manageServer},
	{Name: "timezone", Description: "Your time zone, so /besttime shows your hours", DefaultMemberPermissions: &manageServer,
		Options: []*discordgo.ApplicationCommandOption{str("zone", "e.g. EST, PST, UK, or a name like America/Chicago or Europe/Berlin", true)}},
	{Name: "settings", Description: "How TrendPilot is set up in this server"},
	{Name: "reset", Description: "Delete everything TrendPilot stores for this server", DefaultMemberPermissions: &manageServer},
}

// privateCommands reply only to the person who ran them.
var privateCommands = map[string]bool{
	"mychannel": true, "connect-twitch": true, "disconnect-twitch": true, "timezone": true,
	"settings": true, "reset": true, "sendhere": true, "stopsending": true,
}

// Friendly names for the zones people type most.
var zoneAliases = map[string]string{
	"est": "America/New_York", "edt": "America/New_York", "eastern": "America/New_York", "et": "America/New_York",
	"cst": "America/Chicago", "cdt": "America/Chicago", "central": "America/Chicago", "ct": "America/Chicago",
	"mst": "America/Denver", "mdt": "America/Denver", "mountain": "America/Denver",
	"pst": "America/Los_Angeles", "pdt": "America/Los_Angeles", "pacific": "America/Los_Angeles", "pt": "America/Los_Angeles",
	"akst": "America/Anchorage", "hst": "Pacific/Honolulu", "ast": "America/Halifax",
	"gmt": "Europe/London", "bst": "Europe/London", "uk": "Europe/London", "london": "Europe/London",
	"cet": "Europe/Berlin", "cest": "Europe/Berlin", "eet": "Europe/Athens",
	"ist": "Asia/Kolkata", "jst": "Asia/Tokyo", "kst": "Asia/Seoul", "aest": "Australia/Sydney",
	"brt": "America/Sao_Paulo", "brazil": "America/Sao_Paulo", "utc": "UTC",
}

// ParseZone accepts an alias ("PST") or an IANA name ("Europe/Berlin").
func ParseZone(s string) (*time.Location, error) {
	s = strings.TrimSpace(s)
	if z, ok := zoneAliases[strings.ToLower(s)]; ok {
		s = z
	}
	if s == "" || strings.EqualFold(s, "local") {
		return nil, fmt.Errorf("empty zone")
	}
	return time.LoadLocation(s)
}

// locFor is the guild's time zone, or the instance default.
func (b *Bot) locFor(guildID string) *time.Location {
	if b.Store != nil && guildID != "" {
		if tz := b.Store.Get(guildID).TZ; tz != "" {
			if loc, err := time.LoadLocation(tz); err == nil {
				return loc
			}
		}
	}
	if b.app.Games != nil && b.app.Games.Loc != nil {
		return b.app.Games.Loc
	}
	return time.UTC
}

// watcherFor is the guild's own creator watcher, or the single creator
// configured by TWITCH_CHANNEL on a self-hosted copy.
func (b *Bot) watcherFor(guildID string) *live.Watcher {
	if b.Live != nil {
		if w := b.Live.Get(guildID); w != nil {
			return w
		}
	}
	return b.Watcher
}

type userCache struct {
	mu      sync.Mutex
	byGuild map[string]*twitch.UserAuth
	pending map[string]bool
}

func (b *Bot) tokenPath(guildID string) string {
	return filepath.Join(b.TokenDir, guildID+".json")
}

// UserFor is the guild's Twitch sign-in, or the self-hosted one.
func (b *Bot) UserFor(guildID string) *twitch.UserAuth { return b.userFor(guildID) }

func (b *Bot) userFor(guildID string) *twitch.UserAuth {
	if b.Twitch != nil && b.TokenDir != "" && guildID != "" {
		b.users.mu.Lock()
		defer b.users.mu.Unlock()
		if u, ok := b.users.byGuild[guildID]; ok {
			return u
		}
		if u := b.Twitch.LoadUserAuth(b.tokenPath(guildID)); u != nil {
			b.users.byGuild[guildID] = u
			return u
		}
	}
	// The self-hosted sign-in only counts if it is this server's channel.
	if b.User != nil && b.Store != nil && guildID != "" {
		if ch := b.Store.Get(guildID).TwitchChannel; ch != "" {
			if _, login := b.User.User(); !strings.EqualFold(login, ch) {
				return nil
			}
		}
	}
	return b.User
}

func (b *Bot) hostedAction(ctx context.Context, i *discordgo.InteractionCreate, name string, opt map[string]string) string {
	if i.GuildID == "" {
		return "Use this inside your server, not in DMs."
	}
	if name != "settings" && !b.canManage(i) {
		return "Only members with **Manage Server** can do that."
	}
	switch name {
	case "mychannel":
		return b.myChannel(ctx, i.GuildID, opt["channel"])
	case "connect-twitch":
		return b.connectTwitch(i)
	case "disconnect-twitch":
		return b.disconnectTwitch(i.GuildID)
	case "timezone":
		return b.setTimezone(i.GuildID, opt["zone"])
	case "settings":
		return b.settingsText(i.GuildID)
	case "reset":
		return b.reset(i.GuildID)
	}
	return "Unknown command."
}

func (b *Bot) myChannel(ctx context.Context, guildID, channel string) string {
	if b.Live == nil || b.Twitch == nil || b.Store == nil {
		return "Following your stream needs Twitch keys on this TrendPilot."
	}
	login := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(channel, "https://www.twitch.tv/")), "@"))
	login = strings.TrimPrefix(login, "https://twitch.tv/")
	_, display, err := b.Twitch.UserByLogin(ctx, login)
	if err != nil {
		return "Couldn't find that channel: " + err.Error()
	}
	if b.Live.Set(guildID, login) == nil {
		return "This TrendPilot is following as many channels as it can right now. Try again later."
	}
	if err := b.Store.Update(guildID, func(g *store.Guild) { g.TwitchChannel = login }); err != nil {
		return "Couldn't save that: " + err.Error()
	}
	if u := b.userFor(guildID); u != nil {
		if _, l := u.User(); !strings.EqualFold(l, login) {
			b.dropUser(guildID)
		} else if w := b.Live.Get(guildID); w != nil && u.HasScope("clips:edit") {
			w.SetClipper(u)
		}
	}
	g := b.Store.Get(guildID)
	var next []string
	if g.AnnounceChannel == "" {
		next = append(next, "• `/sendhere what:Go-live announcements` in the channel for go-live posts")
	}
	if g.QueueChannel == "" {
		next = append(next, "• `/sendhere what:Clips & highlights` in a private channel for chat-surge highlights")
	}
	if b.userFor(guildID) == nil {
		next = append(next, "• `/connect-twitch` to turn on automatic clips and 1-click Raid buttons")
	}
	msg := fmt.Sprintf("✅ Following **%s**. I check every minute whether you're live and watch your chat for big moments.", display)
	if len(next) > 0 {
		msg += "\n\nNext:\n" + strings.Join(next, "\n")
	}
	return msg
}

const deviceLoginWindow = 14 * time.Minute // Discord lets us follow up for 15 minutes

func (b *Bot) connectTwitch(i *discordgo.InteractionCreate) string {
	if b.Twitch == nil || b.TokenDir == "" || b.Store == nil {
		return "Twitch sign-in isn't available on this TrendPilot."
	}
	guildID := i.GuildID
	channel := b.Store.Get(guildID).TwitchChannel
	if channel == "" {
		return "First set your channel with `/mychannel`."
	}
	b.users.mu.Lock()
	if b.users.pending[guildID] {
		b.users.mu.Unlock()
		return "A sign-in is already waiting for this server. Use the code from the earlier message, or wait 15 minutes and try again."
	}
	b.users.pending[guildID] = true
	b.users.mu.Unlock()

	shown := make(chan string, 1)
	go func() {
		defer func() { b.users.mu.Lock(); delete(b.users.pending, guildID); b.users.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), deviceLoginWindow)
		defer cancel()
		tok, err := b.Twitch.DeviceLogin(ctx, func(uri, code string) {
			shown <- fmt.Sprintf("**Connect Twitch** (only you can see this)\n1. Open %s while signed in to Twitch as **%s**\n2. Check the code matches: **`%s`**\n3. Click **Authorize**\n\nTrendPilot only asks to *create clips* and *start raids when you press Raid*. You'll get a message here when it's done.", uri, channel, code)
		})
		followUp := func(text string) {
			b.s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{Content: text, Flags: discordgo.MessageFlagsEphemeral})
		}
		if err != nil {
			select {
			case shown <- "Couldn't start Twitch sign-in: " + err.Error():
			default:
				followUp("Twitch sign-in didn't finish: " + err.Error())
			}
			return
		}
		if !strings.EqualFold(tok.Login, channel) {
			followUp(fmt.Sprintf("You signed in as **%s**, but this server follows **%s**. Sign in as %s, or change `/mychannel`. Nothing was saved.", tok.Login, channel, channel))
			return
		}
		if err := twitch.SaveUserToken(b.tokenPath(guildID), tok); err != nil {
			followUp("Signed in, but couldn't save it: " + err.Error())
			return
		}
		b.dropUser(guildID)
		if w := b.watcherFor(guildID); w != nil {
			if u := b.userFor(guildID); u != nil && u.HasScope("clips:edit") {
				w.SetClipper(u)
			}
		}
		followUp(fmt.Sprintf("✅ Connected to Twitch as **%s**. Automatic clips and Raid buttons are on.", tok.Login))
	}()
	select {
	case msg := <-shown:
		return msg
	case <-time.After(20 * time.Second):
		return "Twitch didn't answer. Try again in a minute."
	}
}

func (b *Bot) dropUser(guildID string) {
	b.users.mu.Lock()
	delete(b.users.byGuild, guildID)
	b.users.mu.Unlock()
}

func (b *Bot) disconnectTwitch(guildID string) string {
	if b.TokenDir == "" {
		return "Nothing to disconnect."
	}
	err := os.Remove(b.tokenPath(guildID))
	b.dropUser(guildID)
	if w := b.watcherFor(guildID); w != nil && w != b.Watcher {
		w.SetClipper(nil)
	}
	if err != nil && !os.IsNotExist(err) {
		return "Couldn't remove the sign-in: " + err.Error()
	}
	return "Disconnected. TrendPilot no longer has your Twitch sign-in. To be thorough, also remove it at https://www.twitch.tv/settings/connections."
}

func (b *Bot) setTimezone(guildID, zone string) string {
	loc, err := ParseZone(zone)
	if err != nil {
		return fmt.Sprintf("I don't recognise %q. Try `EST`, `PST`, `UK`, or a name like `America/Chicago`, `Europe/Berlin`, `Asia/Tokyo`.", zone)
	}
	if b.Store == nil {
		return "Settings storage is off."
	}
	if err := b.Store.Update(guildID, func(g *store.Guild) { g.TZ = loc.String() }); err != nil {
		return "Couldn't save that: " + err.Error()
	}
	return fmt.Sprintf("✅ Time zone set to **%s** (it's %s there now). `/besttime` will use it.", loc, time.Now().In(loc).Format("3:04 PM Mon"))
}

func (b *Bot) settingsText(guildID string) string {
	g := store.Guild{}
	if b.Store != nil {
		g = b.Store.Get(guildID)
	}
	ch := func(id string) string {
		if id == "" {
			return "not set"
		}
		return "<#" + id + ">"
	}
	twitchCh := g.TwitchChannel
	if twitchCh == "" {
		twitchCh = "not set (`/mychannel`)"
	} else {
		twitchCh = "**" + twitchCh + "**"
	}
	signedIn := "no (`/connect-twitch`)"
	if u := b.userFor(guildID); u != nil {
		_, l := u.User()
		signedIn = "yes, as **" + l + "**"
	}
	tz := g.TZ
	if tz == "" {
		tz = b.locFor(guildID).String() + " (default; change with `/timezone`)"
	}
	ping := "none"
	if g.AnnounceMention != "" {
		ping = "<@&" + g.AnnounceMention + ">"
	}
	return fmt.Sprintf(`**TrendPilot in this server**
Twitch channel: %s
Twitch sign-in (clips, raids): %s
Time zone: %s
Trend alerts → %s
Clips & highlights → %s
Go-live posts → %s (ping: %s)

Change any of these with /mychannel, /timezone, /sendhere and /stopsending. /reset deletes everything.`,
		twitchCh, signedIn, tz, ch(g.AlertChannel), ch(g.QueueChannel), ch(g.AnnounceChannel), ping)
}

func (b *Bot) reset(guildID string) string {
	if b.Live != nil {
		b.Live.Remove(guildID)
	}
	if b.TokenDir != "" {
		os.Remove(b.tokenPath(guildID))
	}
	b.dropUser(guildID)
	if b.Store != nil {
		if err := b.Store.Delete(guildID); err != nil {
			return "Couldn't delete everything: " + err.Error()
		}
	}
	slog.Info("guild reset", "guild", guildID)
	return "🗑️ Done. TrendPilot no longer stores anything for this server: channel choices, time zone, your Twitch channel and sign-in are all deleted. To remove the bot entirely, kick it from the server."
}

// --- per-server posting ---

// PosterFor returns a live.Poster that posts only in one server.
func (b *Bot) PosterFor(guildID string) live.Poster { return guildPoster{b: b, guild: guildID} }

type guildPoster struct {
	b     *Bot
	guild string
}

func (p guildPoster) GoLive(ctx context.Context, text string) {
	if p.b.Store != nil {
		p.b.goLiveTo(p.b.Store.Get(p.guild), text)
	}
}

func (p guildPoster) Queue(ctx context.Context, it *live.QueueItem) {
	if p.b.Store != nil {
		p.b.queueTo(p.b.Store.Get(p.guild), it)
	}
}

func (p guildPoster) Recap(ctx context.Context, text string) {
	if p.b.Store != nil {
		p.b.recapTo(p.b.Store.Get(p.guild), text)
	}
}

// --- /suggest limits, so one server can't use up a shared AI key ---

type limiter struct {
	mu       sync.Mutex
	perUser  map[string]time.Time // user -> last use
	perGuild map[string]int       // guild -> uses today
	day      string
	Cooldown time.Duration
	Daily    int
}

func newLimiter(cooldown time.Duration, daily int) *limiter {
	return &limiter{perUser: map[string]time.Time{}, perGuild: map[string]int{}, Cooldown: cooldown, Daily: daily}
}

// Allow reports whether user may run an AI command now, and why not.
func (l *limiter) Allow(guildID, userID string, now time.Time) (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if d := now.UTC().Format("2006-01-02"); d != l.day {
		l.day, l.perGuild = d, map[string]int{}
		for u, t := range l.perUser {
			if now.Sub(t) > l.Cooldown {
				delete(l.perUser, u)
			}
		}
	}
	if t, ok := l.perUser[userID]; ok && now.Sub(t) < l.Cooldown {
		return false, fmt.Sprintf("Give it %d more seconds before the next one.", int((l.Cooldown-now.Sub(t)).Seconds())+1)
	}
	if l.Daily > 0 && guildID != "" && l.perGuild[guildID] >= l.Daily {
		return false, fmt.Sprintf("This server has used today's %d AI suggestions. They reset at midnight UTC.", l.Daily)
	}
	l.perUser[userID] = now
	if guildID != "" {
		l.perGuild[guildID]++
	}
	return true, ""
}
