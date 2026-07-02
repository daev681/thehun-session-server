package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Info는 하나의 활성 세션을 나타냅니다.
type Info struct {
	AccountID string
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (i *Info) Expired() bool {
	return time.Now().After(i.ExpiresAt)
}

// Store는 토큰 -> 세션 정보 매핑을 스레드 안전하게 관리합니다.
// 동일 계정으로 재로그인 시 이전 토큰은 자동으로 무효화됩니다.
type Store struct {
	mu             sync.RWMutex
	sessions       map[string]*Info  // token -> Info
	accountToToken map[string]string // accountID -> token
	ttl            time.Duration
}

func NewStore(ttl time.Duration) *Store {
	return &Store{
		sessions:       make(map[string]*Info),
		accountToToken: make(map[string]string),
		ttl:            ttl,
	}
}

func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Create는 accountID에 대한 새 세션 토큰을 발급합니다.
func (s *Store) Create(accountID string) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if old, ok := s.accountToToken[accountID]; ok {
		delete(s.sessions, old)
	}

	now := time.Now()
	s.sessions[token] = &Info{
		AccountID: accountID,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}
	s.accountToToken[accountID] = token
	return token, nil
}

// Get은 토큰으로 세션 정보를 조회합니다. 만료된 세션은 자동 삭제합니다.
func (s *Store) Get(token string) (*Info, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, ok := s.sessions[token]
	if !ok {
		return nil, false
	}
	if info.Expired() {
		delete(s.accountToToken, info.AccountID)
		delete(s.sessions, token)
		return nil, false
	}
	return info, true
}

// Refresh는 기존 토큰을 무효화하고 새 토큰을 발급합니다. TTL이 갱신됩니다.
func (s *Store) Refresh(oldToken string) (string, error) {
	newToken, err := generateToken()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	info, ok := s.sessions[oldToken]
	if !ok {
		return "", errors.New("session not found")
	}
	if info.Expired() {
		delete(s.accountToToken, info.AccountID)
		delete(s.sessions, oldToken)
		return "", errors.New("session expired")
	}

	now := time.Now()
	newInfo := &Info{
		AccountID: info.AccountID,
		CreatedAt: info.CreatedAt,
		ExpiresAt: now.Add(s.ttl),
	}

	delete(s.sessions, oldToken)
	s.sessions[newToken] = newInfo
	s.accountToToken[info.AccountID] = newToken
	return newToken, nil
}

// Delete는 토큰을 무효화합니다.
func (s *Store) Delete(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, ok := s.sessions[token]
	if !ok {
		return errors.New("session not found")
	}
	delete(s.accountToToken, info.AccountID)
	delete(s.sessions, token)
	return nil
}

// Cleanup은 만료된 세션을 모두 제거합니다. 주기적으로 호출하세요.
func (s *Store) Cleanup() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	now := time.Now()
	for token, info := range s.sessions {
		if now.After(info.ExpiresAt) {
			delete(s.accountToToken, info.AccountID)
			delete(s.sessions, token)
			removed++
		}
	}
	return removed
}

// Count는 현재 활성 세션 수를 반환합니다.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}
