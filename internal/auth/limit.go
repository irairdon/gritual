package auth

import (
	"sync"
	"time"
)

type limiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
	magic map[string][]time.Time
}

func newLimiter() *limiter {
	return &limiter{
		fails: make(map[string][]time.Time),
		magic: make(map[string][]time.Time),
	}
}

func (l *limiter) loginLocked(ip string) bool {
	return l.over(l.fails, ip, 10, 15*time.Minute)
}

func (l *limiter) loginFail(ip string) {
	l.add(l.fails, ip)
}

func (l *limiter) magicLimited(email string) bool {
	return l.over(l.magic, email, 5, time.Hour)
}

func (l *limiter) magicHit(email string) {
	l.add(l.magic, email)
}

func (l *limiter) over(store map[string][]time.Time, key string, n int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	store[key] = pruneTimes(store[key], now.Add(-window))
	return len(store[key]) >= n
}

func (l *limiter) add(store map[string][]time.Time, key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	store[key] = append(store[key], time.Now())
}

func pruneTimes(in []time.Time, cutoff time.Time) []time.Time {
	out := in[:0]
	for _, t := range in {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
