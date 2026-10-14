// Package store keeps the few settings that must survive a restart (which
// Discord channel gets which posts, muted alerts) in one small JSON file.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Guild is one Discord server's TrendPilot settings.
type Guild struct {
	AlertChannel    string               `json:"alert_channel,omitempty"`    // category shifts
	QueueChannel    string               `json:"queue_channel,omitempty"`    // clips and highlights awaiting approval
	AnnounceChannel string               `json:"announce_channel,omitempty"` // go-live posts
	AnnounceMention string               `json:"announce_mention,omitempty"` // "", "@here", or a role ID
	Muted           map[string]time.Time `json:"muted,omitempty"`            // alert key -> muted until
	TZ              string               `json:"tz,omitempty"`               // IANA zone for /besttime
	TwitchChannel   string               `json:"twitch_channel,omitempty"`   // the creator's channel (/mychannel)
}

type Store struct {
	mu     sync.Mutex
	wmu    sync.Mutex // serializes file writes
	path   string
	Guilds map[string]*Guild `json:"guilds"`
}

// Open loads path, or starts empty. An empty path keeps settings in memory.
func Open(path string) *Store {
	s := &Store{path: path, Guilds: map[string]*Guild{}}
	if path == "" {
		return s
	}
	if b, err := os.ReadFile(path); err == nil {
		var loaded Store
		if json.Unmarshal(b, &loaded) == nil && loaded.Guilds != nil {
			s.Guilds = loaded.Guilds
		}
	}
	return s
}

// Update changes one guild's settings and saves.
func (s *Store) Update(guildID string, fn func(g *Guild)) error {
	s.mu.Lock()
	g := s.Guilds[guildID]
	if g == nil {
		g = &Guild{}
		s.Guilds[guildID] = g
	}
	fn(g)
	s.mu.Unlock()
	return s.save()
}

// Delete forgets everything stored for a guild.
func (s *Store) Delete(guildID string) error {
	s.mu.Lock()
	delete(s.Guilds, guildID)
	s.mu.Unlock()
	return s.save()
}

func (s *Store) save() error {
	s.mu.Lock()
	b, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil || s.path == "" {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Get returns a copy of one guild's settings.
func (s *Store) Get(guildID string) Guild {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g := s.Guilds[guildID]; g != nil {
		c := *g
		c.Muted = make(map[string]time.Time, len(g.Muted))
		for k, v := range g.Muted {
			c.Muted[k] = v
		}
		return c
	}
	return Guild{}
}

// Each calls fn with a copy of every guild's settings.
func (s *Store) Each(fn func(guildID string, g Guild)) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.Guilds))
	for id := range s.Guilds {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		fn(id, s.Get(id))
	}
}

func (g Guild) IsMuted(key string, now time.Time) bool {
	until, ok := g.Muted[key]
	return ok && now.Before(until)
}
