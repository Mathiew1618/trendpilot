package live

import (
	"context"
	"sync"
	"testing"

	"trendpilot/internal/twitch"
)

type batchSrc struct {
	mu    sync.Mutex
	live  map[string]bool
	calls int
	asked []string
}

func (b *batchSrc) StreamsByLogins(ctx context.Context, logins []string) (map[string]twitch.Stream, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	b.asked = logins
	out := map[string]twitch.Stream{}
	for _, l := range logins {
		if b.live[l] {
			out[l] = twitch.Stream{UserLogin: l, GameName: "Hades II"}
		}
	}
	return out, nil
}

func TestManagerRoutesPostsPerGuild(t *testing.T) {
	src := &batchSrc{live: map[string]bool{"alice": true}}
	posters := map[string]*fakePoster{"g1": {}, "g2": {}, "g3": {}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := NewManager(ctx, src, func(guildID, channel string) *Watcher {
		return NewWatcher(channel, nil, posters[guildID], nil, nil, nil, nil)
	})
	m.StartChat = func(context.Context, *Watcher) {} // stay offline
	m.Set("g1", "Alice")
	m.Set("g2", "@bob")
	m.Set("g3", "alice") // two servers can follow the same creator

	if err := m.PollAll(ctx); err != nil {
		t.Fatal(err)
	}
	if src.calls != 1 || len(src.asked) != 2 {
		t.Fatalf("want one batched call for 2 unique channels, got calls=%d asked=%v", src.calls, src.asked)
	}
	if len(posters["g1"].golive) != 1 || len(posters["g3"].golive) != 1 || len(posters["g2"].golive) != 0 {
		t.Fatalf("go-live routing g1=%d g2=%d g3=%d", len(posters["g1"].golive), len(posters["g2"].golive), len(posters["g3"].golive))
	}

	// Switching g1 to bob replaces its watcher; removing g3 stops it.
	if w := m.Set("g1", "bob"); w == nil || w.Channel != "bob" {
		t.Fatal("replace failed")
	}
	m.Remove("g3")
	if m.Len() != 2 || m.Get("g3") != nil {
		t.Fatalf("len=%d", m.Len())
	}
}
