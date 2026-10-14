// Package cache is a small in-memory TTL cache. Nothing is ever written to
// disk: state lives only for the life of the process.
package cache

import (
	"context"
	"sync"
	"time"
)

type entry[V any] struct {
	val     V
	expires time.Time
}

// TTL is a concurrency-safe map whose entries expire after a fixed duration.
type TTL[K comparable, V any] struct {
	mu    sync.RWMutex
	items map[K]entry[V]
	ttl   time.Duration
	now   func() time.Time
}

func New[K comparable, V any](ttl time.Duration) *TTL[K, V] {
	return &TTL[K, V]{items: make(map[K]entry[V]), ttl: ttl, now: time.Now}
}

func (c *TTL[K, V]) Get(k K) (V, bool) {
	c.mu.RLock()
	e, ok := c.items[k]
	c.mu.RUnlock()
	if !ok || c.now().After(e.expires) {
		var zero V
		return zero, false
	}
	return e.val, true
}

func (c *TTL[K, V]) Set(k K, v V) {
	c.mu.Lock()
	c.items[k] = entry[V]{val: v, expires: c.now().Add(c.ttl)}
	c.mu.Unlock()
}

func (c *TTL[K, V]) Delete(k K) {
	c.mu.Lock()
	delete(c.items, k)
	c.mu.Unlock()
}

func (c *TTL[K, V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Sweep removes expired entries so memory does not grow without bound.
func (c *TTL[K, V]) Sweep() {
	now := c.now()
	c.mu.Lock()
	for k, e := range c.items {
		if now.After(e.expires) {
			delete(c.items, k)
		}
	}
	c.mu.Unlock()
}

// Janitor sweeps every interval until ctx is cancelled.
func (c *TTL[K, V]) Janitor(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Sweep()
		}
	}
}
