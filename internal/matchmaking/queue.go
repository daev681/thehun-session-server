package matchmaking

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// GameServerAddr는 매칭된 게임서버의 주소입니다.
type GameServerAddr struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// Status는 매치메이킹 티켓의 상태입니다.
type Status int

const (
	StatusQueued   Status = iota // 큐에 대기 중
	StatusMatched                // 매칭 완료
	StatusNotFound               // 큐에 없음
)

// Ticket은 매치메이킹 요청 하나를 나타냅니다.
type Ticket struct {
	ID         string
	AccountID  string
	GameMode   string
	QueuedAt   time.Time
	Status     Status
	GameServer *GameServerAddr // Status == Matched일 때만 유효
	MatchedAt  time.Time
}

// Queue는 FIFO 기반 매치메이킹 큐입니다.
// playersPerMatch 명이 모이면 자동으로 매칭합니다.
type Queue struct {
	mu              sync.Mutex
	tickets         map[string]*Ticket  // ticketID -> Ticket
	accountToTicket map[string]string   // accountID -> ticketID
	waiting         []*Ticket
	playersPerMatch int
	gameServer      GameServerAddr
	matchedTTL      time.Duration // 매칭 완료 티켓 유지 시간
}

func NewQueue(playersPerMatch int, gameServerHost string, gameServerPort int) *Queue {
	return &Queue{
		tickets:         make(map[string]*Ticket),
		accountToTicket: make(map[string]string),
		waiting:         make([]*Ticket, 0, 64),
		playersPerMatch: playersPerMatch,
		gameServer:      GameServerAddr{Host: gameServerHost, Port: gameServerPort},
		matchedTTL:      60 * time.Second,
	}
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck — crypto/rand.Read은 실패하지 않음
	return hex.EncodeToString(b)
}

// Enter는 accountID를 큐에 등록하고 티켓 ID를 반환합니다.
// 이미 큐에 있으면 기존 티켓 ID를 반환합니다.
func (q *Queue) Enter(accountID, gameMode string) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if ticketID, ok := q.accountToTicket[accountID]; ok {
		return ticketID, nil // 이미 대기 중
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

// Cancel은 대기 중인 티켓을 취소합니다.
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

	// waiting 슬라이스에서 제거 (순서 유지)
	for i, wt := range q.waiting {
		if wt.ID == ticketID {
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			break
		}
	}
	return nil
}

// GetTicket은 accountID의 현재 티켓을 반환합니다.
func (q *Queue) GetTicket(accountID string) (*Ticket, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	ticketID, ok := q.accountToTicket[accountID]
	if !ok {
		return nil, false
	}
	return q.tickets[ticketID], true
}

// QueueLength는 대기 중인 플레이어 수입니다.
func (q *Queue) QueueLength() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.waiting)
}

// CleanupExpired는 TTL이 지난 매칭 완료 티켓을 정리합니다.
// 주기적으로 goroutine에서 호출하세요.
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
		group := q.waiting[:q.playersPerMatch]
		q.waiting = q.waiting[q.playersPerMatch:]

		addr := q.gameServer
		now := time.Now()
		for _, t := range group {
			t.Status = StatusMatched
			t.GameServer = &addr
			t.MatchedAt = now
		}
	}
}
