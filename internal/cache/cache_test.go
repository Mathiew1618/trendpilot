package cache

import (
	"testing"
	"time"
)

func TestExpiry(t *testing.T) {
	c := New[string, int](time.Minute)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }

	c.Set("a", 1)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("got %v %v, want 1 true", v, ok)
	}
	now = now.Add(2 * time.Minute)
	if _, ok := c.Get("a"); ok {
		t.Fatal("entry should have expired")
	}
	c.Sweep()
	if c.Len() != 0 {
		t.Fatalf("sweep left %d entries", c.Len())
	}
}
