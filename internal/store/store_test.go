package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPersistAndMute(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	s := Open(p)
	now := time.Now()
	if err := s.Update("g1", func(g *Guild) {
		g.AlertChannel = "c1"
		g.Muted = map[string]time.Time{"genre:horror": now.Add(time.Hour)}
	}); err != nil {
		t.Fatal(err)
	}
	g := Open(p).Get("g1")
	if g.AlertChannel != "c1" || !g.IsMuted("genre:horror", now) || g.IsMuted("genre:horror", now.Add(2*time.Hour)) {
		t.Fatalf("reloaded %+v", g)
	}
	// Get returns a copy: mutating it must not touch the store.
	g.Muted["x"] = now
	if s.Get("g1").IsMuted("x", now.Add(-time.Second)) {
		t.Fatal("Get leaked internal map")
	}
}
