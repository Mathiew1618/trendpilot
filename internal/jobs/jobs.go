// Package jobs runs TrendPilot's background work on fixed intervals. Each job
// runs in its own goroutine; a panic or error in one job is logged and never
// stops the others.
package jobs

import (
	"context"
	"log/slog"
	"math/rand"
	"runtime/debug"
	"sync"
	"time"
)

type Job struct {
	Name  string
	Every time.Duration
	// Delay before the first run. Staggering jobs keeps start-up from
	// firing every API call in the same second.
	Delay time.Duration
	Run   func(ctx context.Context) error
}

type Scheduler struct {
	mu   sync.Mutex
	jobs []Job
	last map[string]Status
}

type Status struct {
	Name     string        `json:"name"`
	Every    string        `json:"every"`
	LastRun  time.Time     `json:"last_run"`
	Took     time.Duration `json:"took_ns"`
	LastErr  string        `json:"last_error,omitempty"`
	Runs     int           `json:"runs"`
	Failures int           `json:"failures"`
}

func New() *Scheduler { return &Scheduler{last: map[string]Status{}} }

func (s *Scheduler) Add(j Job) { s.mu.Lock(); s.jobs = append(s.jobs, j); s.mu.Unlock() }

// Start launches every job and returns immediately.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	jobs := append([]Job(nil), s.jobs...)
	s.mu.Unlock()
	for _, j := range jobs {
		go s.loop(ctx, j)
	}
}

func (s *Scheduler) loop(ctx context.Context, j Job) {
	if !sleep(ctx, j.Delay) {
		return
	}
	for {
		s.runOnce(ctx, j)
		// Up to 5% jitter so jobs that share an interval drift apart.
		next := j.Every + time.Duration(rand.Int63n(int64(j.Every)/20+1))
		if !sleep(ctx, next) {
			return
		}
	}
}

func (s *Scheduler) runOnce(ctx context.Context, j Job) {
	start := time.Now()
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("job panicked", "job", j.Name, "panic", r, "stack", string(debug.Stack()))
				err = errPanic
			}
		}()
		err = j.Run(ctx)
	}()
	if err != nil && ctx.Err() == nil {
		slog.Warn("job failed", "job", j.Name, "err", err)
	}
	s.mu.Lock()
	st := s.last[j.Name]
	st.Name, st.Every, st.LastRun, st.Took = j.Name, j.Every.String(), start, time.Since(start)
	st.Runs++
	st.LastErr = ""
	if err != nil {
		st.Failures++
		st.LastErr = err.Error()
	}
	s.last[j.Name] = st
	s.mu.Unlock()
}

type panicErr struct{}

func (panicErr) Error() string { return "job panicked" }

var errPanic = panicErr{}

func (s *Scheduler) Status() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Status, 0, len(s.jobs))
	for _, j := range s.jobs {
		st := s.last[j.Name]
		st.Name, st.Every = j.Name, j.Every.String()
		out = append(out, st)
	}
	return out
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
