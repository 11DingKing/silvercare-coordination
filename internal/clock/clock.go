package clock

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC() }

type Fixed struct {
	mu  sync.RWMutex
	now time.Time
}

func NewFixed(now time.Time) *Fixed {
	return &Fixed{now: now.UTC()}
}

func (f *Fixed) Now() time.Time {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.now
}

func (f *Fixed) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *Fixed) Set(now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = now.UTC()
}
