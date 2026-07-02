package registry

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

var ErrNotFound = errors.New("game server not found")

// GameServer는 등록된 리얼타임 서버 하나를 나타냅니다.
type GameServer struct {
	ID             string    `json:"id"`
	Host           string    `json:"host"`
	Port           int       `json:"port"`
	MaxPlayers     int       `json:"max_players"`
	CurrentPlayers int       `json:"current_players"`
	RegisteredAt   time.Time `json:"registered_at"`
	LastHeartbeat  time.Time `json:"last_heartbeat"`
}

// IsHealthy는 최근 30초 이내에 heartbeat를 받았으면 true를 반환합니다.
func (g *GameServer) IsHealthy() bool {
	return time.Since(g.LastHeartbeat) < 30*time.Second
}

func (g *GameServer) Available() int {
	return g.MaxPlayers - g.CurrentPlayers
}

// Store는 게임 서버 레지스트리입니다.
type Store struct {
	mu      sync.RWMutex
	servers map[string]*GameServer
}

func NewStore() *Store {
	return &Store{servers: make(map[string]*GameServer)}
}

// Register는 새 게임 서버를 등록하고 서버 ID를 발급합니다.
func (s *Store) Register(host string, port, maxPlayers int) (*GameServer, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}

	gs := &GameServer{
		ID:            hex.EncodeToString(b),
		Host:          host,
		Port:          port,
		MaxPlayers:    maxPlayers,
		LastHeartbeat: time.Now(),
		RegisteredAt:  time.Now(),
	}

	s.mu.Lock()
	s.servers[gs.ID] = gs
	s.mu.Unlock()
	return gs, nil
}

// Heartbeat는 서버의 현재 접속자 수와 생존 신호를 업데이트합니다.
func (s *Store) Heartbeat(id string, currentPlayers int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	gs, ok := s.servers[id]
	if !ok {
		return ErrNotFound
	}
	gs.LastHeartbeat = time.Now()
	gs.CurrentPlayers = currentPlayers
	return nil
}

// Unregister는 게임 서버를 제거합니다.
func (s *Store) Unregister(id string) {
	s.mu.Lock()
	delete(s.servers, id)
	s.mu.Unlock()
}

// GetBest는 가장 여유 있는 건강한 서버를 반환합니다. 없으면 nil.
func (s *Store) GetBest() *GameServer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var best *GameServer
	for _, gs := range s.servers {
		if !gs.IsHealthy() || gs.Available() <= 0 {
			continue
		}
		if best == nil || gs.CurrentPlayers < best.CurrentPlayers {
			cp := *gs
			best = &cp
		}
	}
	return best
}

// List는 모든 서버의 스냅샷을 반환합니다.
func (s *Store) List() []*GameServer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*GameServer, 0, len(s.servers))
	for _, gs := range s.servers {
		cp := *gs
		out = append(out, &cp)
	}
	return out
}

// PruneUnhealthy는 60초 이상 heartbeat가 없는 서버를 제거합니다.
func (s *Store) PruneUnhealthy() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for id, gs := range s.servers {
		if time.Since(gs.LastHeartbeat) > 60*time.Second {
			delete(s.servers, id)
			removed++
		}
	}
	return removed
}
