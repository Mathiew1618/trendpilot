package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestJobsRunRecoverAndReport(t *testing.T) {
	var ok, bad atomic.Int32
	s := New()
	s.Add(Job{Name: "ok", Every: 10 * time.Millisecond, Run: func(context.Context) error { ok.Add(1); return nil }})
	s.Add(Job{Name: "panics", Every: 10 * time.Millisecond, Run: func(context.Context) error { bad.Add(1); panic("boom") }})
	s.Add(Job{Name: "errs", Every: time.Hour, Delay: time.Millisecond, Run: func(context.Context) error { return errors.New("nope") }})

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	s.Start(ctx)
	<-ctx.Done()
	time.Sleep(5 * time.Millisecond)

	if ok.Load() < 3 || bad.Load() < 3 {
		t.Fatalf("ok=%d panics=%d: a panicking job must not stop either job", ok.Load(), bad.Load())
	}
	for _, st := range s.Status() {
		switch st.Name {
		case "panics":
			if st.Failures == 0 || st.LastErr == "" {
				t.Errorf("panics status %+v", st)
			}
		case "errs":
			if st.Runs != 1 || st.LastErr != "nope" {
				t.Errorf("errs status %+v", st)
			}
		}
	}
}
