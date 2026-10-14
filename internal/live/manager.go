package live

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"trendpilot/internal/twitch"
)

// BatchSource checks many channels in one request.
type BatchSource interface {
	StreamsByLogins(ctx context.Context, logins []string) (map[string]twitch.Stream, error)
}

// Manager runs one Watcher per Discord server, so a hosted TrendPilot can
// follow every creator who set /mychannel. Live checks for all of them go
// out in one batched Twitch request per minute.
type Manager struct {
	// StartChat begins reading a watcher's chat; tests replace it.
	StartChat func(ctx context.Context, w *Watcher)

	src     BatchSource
	newW    func(guildID, channel string) *Watcher
	mu      sync.Mutex
	ctx     context.Context
	byGuild map[string]*managed
}

type managed struct {
	w      *Watcher
	cancel context.CancelFunc
}

// MaxChannels bounds how many creators one hosted instance follows (one
// chat connection each).
const MaxChannels = 500

// NewManager: newWatcher builds a Watcher whose posts go to that guild only.
// ctx bounds the chat readers.
func NewManager(ctx context.Context, src BatchSource, newWatcher func(guildID, channel string) *Watcher) *Manager {
	return &Manager{src: src, newW: newWatcher, ctx: ctx, byGuild: map[string]*managed{},
		StartChat: func(ctx context.Context, w *Watcher) { w.RunChat(ctx) }}
}

// Set follows channel for a guild, replacing any previous channel.
func (m *Manager) Set(guildID, channel string) *Watcher {
	channel = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(channel), "@"))
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.byGuild[guildID]; ok {
		if cur.w.Channel == channel {
			return cur.w
		}
		cur.cancel()
	} else if len(m.byGuild) >= MaxChannels {
		return nil
	}
	w := m.newW(guildID, channel)
	ctx, cancel := context.WithCancel(m.ctx)
	go m.StartChat(ctx, w)
	m.byGuild[guildID] = &managed{w: w, cancel: cancel}
	return w
}

func (m *Manager) Remove(guildID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.byGuild[guildID]; ok {
		cur.cancel()
		delete(m.byGuild, guildID)
	}
}

func (m *Manager) Get(guildID string) *Watcher {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.byGuild[guildID]; ok {
		return cur.w
	}
	return nil
}

func (m *Manager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byGuild)
}

// PollAll checks every followed channel in batches and updates watchers.
func (m *Manager) PollAll(ctx context.Context) error {
	m.mu.Lock()
	ws := make([]*Watcher, 0, len(m.byGuild))
	logins := make([]string, 0, len(m.byGuild))
	seen := map[string]bool{}
	for _, c := range m.byGuild {
		ws = append(ws, c.w)
		if !seen[c.w.Channel] {
			seen[c.w.Channel] = true
			logins = append(logins, c.w.Channel)
		}
	}
	m.mu.Unlock()
	if len(logins) == 0 {
		return nil
	}
	live, err := m.src.StreamsByLogins(ctx, logins)
	if err != nil {
		return err
	}
	for _, w := range ws {
		st, online := live[w.Channel]
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("watcher panicked", "channel", w.Channel, "panic", r)
				}
			}()
			w.Observe(ctx, st, online)
		}()
	}
	return nil
}
