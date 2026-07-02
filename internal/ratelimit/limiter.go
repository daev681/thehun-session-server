package ratelimit

import (
	"sync"
	"time"
)

type entry struct {
	count   int
	resetAt time.Time
}

// Limiter는 키(IP 주소 등)별로 슬라이딩 윈도우 rate limit을 적용합니다.
type Limiter struct {
	mu      sync.Mutex
	entries map[string]*entry
	maxReqs int
	window  time.Duration
}

func New(maxReqs int, window time.Duration) *Limiter {
	return &Limiter{
		entries: make(map[string]*entry),
		maxReqs: maxReqs,
		window:  window,
	}
}

// Allow는 key가 이번 윈도우에서 한 번 더 요청할 수 있으면 true를 반환합니다.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	e, ok := l.entries[key]
	if !ok || now.After(e.resetAt) {
		l.entries[key] = &entry{count: 1, resetAt: now.Add(l.window)}
		return true
	}
	if e.count >= l.maxReqs {
		return false
	}
	e.count++
	return true
}

// Cleanup은 만료된 엔트리를 제거합니다. 주기적으로 호출하세요.
func (l *Limiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, e := range l.entries {
		if now.After(e.resetAt) {
			delete(l.entries, k)
		}
	}
}
