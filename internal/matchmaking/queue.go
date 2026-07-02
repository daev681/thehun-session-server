package matchmaking

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/thehun/session-server/internal/registry"
)

// GameServerAddr는 매칭된 게임서버의 주소입니다.
type GameServerAddr struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type Status int

const (
	StatusQueued  Status = iota
	StatusMatched
)

// Ticket은 매치메이킹 요청 하나를 나타냅니다.
type Ticket struct {
	ID         string
	AccountID  string
	GameMode   string
	QueuedAt   time.Time
	Status     Status
	GameServer *GameServerAddr
	MatchedAt  time.Time
}

// Queue는 FIFO 기반 매치메이킹 큐입니다.
// registry.Store에서 가장 여유 있는 서버를 선택합니다.
type Queue struct {
	mu              sync.Mutex
	tickets         map[string]*Ticket
	accountToTicket map[string]string
	waiting         []*Ticket
	playersPerMatch int
	registry        *registry.Store
	matchedTTL      time.Duration
}

func NewQueue(playersPerMatch int, reg *registry.Store) *Queue {
	return &Queue{
		tickets:         make(map[string]*Ticket),
		accountToTicket: make(map[string]string),
		waiting:         make([]*Ticket, 0, 64),
		playersPerMatch: playersPerMatch,
		registry:        reg,
		matchedTTL:      60 * time.Second,
	}
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}

// Enter는 accountID를 큐에 등록하고 티켓 ID를 반환합니다.
func (q *Queue) Enter(accountID, gameMode string) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if ticketID, ok := q.accountToTicket[accountID]; ok {
		return ticketID, nil
	}

	ticketID := generateID()
	t := &Ticket{
		ID:        ticketID,
		AccountID: accountID,
		GameMode:  gameMode,
		QueuedAt:  time.Now(),
		Status:    StatusQueued,
	}

	q.tickets[ticketID] = t
	q.accountToTicket[accountID] = ticketID
	q.waiting = append(q.waiting, t)

	q.tryMatch()
	return ticketID, nil
}

func (q *Queue) Cancel(accountID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	ticketID, ok := q.accountToTicket[accountID]
	if !ok {
		return errors.New("not in queue")
	}

	t := q.tickets[ticketID]
	if t.Status == StatusMatched {
		return errors.New("already matched — cannot cancel")
	}

	delete(q.tickets, ticketID)
	delete(q.accountToTicket, accountID)

	for i, wt := range q.waiting {
		if wt.ID == ticketID {
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			break
		}
	}
	return nil
}

func (q *Queue) GetTicket(accountID string) (*Ticket, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	ticketID, ok := q.accountToTicket[accountID]
	if !ok {
		return nil, false
	}
	return q.tickets[ticketID], true
}

func (q *Queue) QueueLength() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.waiting)
}

func (q *Queue) CleanupExpired() {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	for ticketID, t := range q.tickets {
		if t.Status == StatusMatched && now.Sub(t.MatchedAt) > q.matchedTTL {
			delete(q.accountToTicket, t.AccountID)
			delete(q.tickets, ticketID)
		}
	}
}

// tryMatch는 락을 보유한 상태에서 호출해야 합니다.
func (q *Queue) tryMatch() {
	for len(q.waiting) >= q.playersPerMatch {
		gs := q.registry.GetBest()
		if gs == nil {
			return // 가용 서버 없음 - 대기
		}

		group := q.waiting[:q.playersPerMatch]
		q.waiting = q.waiting[q.playersPerMatch:]

		addr := GameServerAddr{Host: gs.Host, Port: gs.Port}
		now := time.Now()
		for _, t := range group {
			t.Status = StatusMatched
			t.GameServer = &addr
			t.MatchedAt = now
		}
	}
}
