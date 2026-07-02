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
}

// Store는 토큰 -> 세션 정보 매핑을 스레드 안전하게 관리합니다.
// 동일 계정으로 재로그인 시 이전 토큰은 자동으로 무효화됩니다.
type Store struct {
	mu             sync.RWMutex
	sessions       map[string]*Info  // token -> Info
	accountToToken map[string]string // accountID -> token (중복 로그인 방지)
}

func NewStore() *Store {
	return &Store{
		sessions:       make(map[string]*Info),
		accountToToken: make(map[string]string),
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
// 기존 토큰이 있으면 먼저 삭제합니다.
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

	s.sessions[token] = &Info{AccountID: accountID, CreatedAt: time.Now()}
	s.accountToToken[accountID] = token
	return token, nil
}

// Get은 토큰으로 세션 정보를 조회합니다.
func (s *Store) Get(token string) (*Info, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info, ok := s.sessions[token]
	return info, ok
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

// Count는 현재 활성 세션 수를 반환합니다.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}
