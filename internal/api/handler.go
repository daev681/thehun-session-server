package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/thehun/session-server/internal/account"
	"github.com/thehun/session-server/internal/matchmaking"
	"github.com/thehun/session-server/internal/ratelimit"
	"github.com/thehun/session-server/internal/registry"
	"github.com/thehun/session-server/internal/session"
)

type Handler struct {
	sessions    *session.Store
	queue       *matchmaking.Queue
	accounts    *account.Repo
	registry    *registry.Store
	loginLimiter *ratelimit.Limiter
}

func NewHandler(
	sessions *session.Store,
	queue *matchmaking.Queue,
	accounts *account.Repo,
	reg *registry.Store,
) *Handler {
	return &Handler{
		sessions:     sessions,
		queue:        queue,
		accounts:     accounts,
		registry:     reg,
		loginLimiter: ratelimit.New(10, 60_000_000_000), // 10 req/min per IP
	}
}

// --- 공통 헬퍼 ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (h *Handler) requireSession(r *http.Request) (*session.Info, string, bool) {
	token := r.Header.Get("X-Session-Token")
	if token == "" {
		return nil, "", false
	}
	info, ok := h.sessions.Get(token)
	return info, token, ok
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.SplitN(fwd, ",", 2)[0]
	}
	if real := r.Header.Get("X-Real-IP"); real != "" {
		return real
	}
	// Strip port from RemoteAddr
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i != -1 {
		return addr[:i]
	}
	return addr
}

// --- Auth 핸들러 ---

// POST /auth/register
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID   string `json:"account_id"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.AccountID == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "account_id and password are required")
		return
	}
	if len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, "password must be at least 6 characters")
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = req.AccountID
	}

	acc, err := h.accounts.Create(r.Context(), req.AccountID, req.Password, req.DisplayName)
	if err != nil {
		if errors.Is(err, account.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "account_id already taken")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create account")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"account_id":   acc.AccountID,
		"display_name": acc.DisplayName,
		"created_at":   acc.CreatedAt,
	})
}

// POST /auth/login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !h.loginLimiter.Allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}

	var req struct {
		AccountID string `json:"account_id"`
		Password  string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.AccountID == "" {
		writeError(w, http.StatusBadRequest, "account_id is required")
		return
	}

	_, err := h.accounts.Authenticate(r.Context(), req.AccountID, req.Password)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) || errors.Is(err, account.ErrWrongPassword) {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeError(w, http.StatusInternalServerError, "auth error")
		return
	}

	token, err := h.sessions.Create(req.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"session_token": token,
		"account_id":    req.AccountID,
	})
}

// POST /auth/refresh
// 토큰 만료 전 갱신. 기존 토큰 무효화 후 새 토큰 발급 (TTL 리셋).
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	_, oldToken, ok := h.requireSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or expired session token")
		return
	}

	newToken, err := h.sessions.Refresh(oldToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "refresh failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"session_token": newToken})
}

// POST /auth/logout
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	_, token, ok := h.requireSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or missing session token")
		return
	}
	h.sessions.Delete(token) //nolint:errcheck
	writeJSON(w, http.StatusOK, map[string]string{})
}

// GET /auth/me
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	info, _, ok := h.requireSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or missing session token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account_id": info.AccountID,
		"expires_at": info.ExpiresAt,
	})
}

// GET /auth/validate?token=<token>
func (h *Handler) ValidateToken(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	info, ok := h.sessions.Get(token)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":      true,
		"account_id": info.AccountID,
	})
}

// --- Player 핸들러 ---

// GET /player/stats
func (h *Handler) PlayerStats(w http.ResponseWriter, r *http.Request) {
	info, _, ok := h.requireSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or missing session token")
		return
	}

	acc, err := h.accounts.GetByAccountID(r.Context(), info.AccountID)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"account_id":   acc.AccountID,
		"display_name": acc.DisplayName,
		"wins":         acc.Wins,
		"losses":       acc.Losses,
		"created_at":   acc.CreatedAt,
	})
}

// POST /player/result
func (h *Handler) PlayerResult(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Winner string   `json:"winner"`
		Losers []string `json:"losers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Winner == "" {
		writeError(w, http.StatusBadRequest, "winner is required")
		return
	}

	if err := h.accounts.AddResult(r.Context(), req.Winner, req.Losers); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update result")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{})
}

// --- Game Server Registry 핸들러 ---

// POST /game-server/register
// 리얼타임서버가 시작 시 자신을 등록합니다.
// Body: {"host":"1.2.3.4","port":7777,"max_players":100}
func (h *Handler) RegisterGameServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host       string `json:"host"`
		Port       int    `json:"port"`
		MaxPlayers int    `json:"max_players"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Host == "" || req.Port == 0 {
		writeError(w, http.StatusBadRequest, "host and port are required")
		return
	}
	if req.MaxPlayers <= 0 {
		req.MaxPlayers = 100
	}

	gs, err := h.registry.Register(req.Host, req.Port, req.MaxPlayers)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "register failed")
		return
	}
	writeJSON(w, http.StatusOK, gs)
}

// POST /game-server/heartbeat
// 리얼타임서버가 주기적으로 현재 접속자 수를 보고합니다.
// Body: {"id":"abc123","current_players":42}
func (h *Handler) GameServerHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID             string `json:"id"`
		CurrentPlayers int    `json:"current_players"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if err := h.registry.Heartbeat(req.ID, req.CurrentPlayers); err != nil {
		writeError(w, http.StatusNotFound, "game server not found — re-register")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

// POST /game-server/unregister
// 리얼타임서버 셧다운 시 호출합니다.
func (h *Handler) UnregisterGameServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	h.registry.Unregister(req.ID)
	writeJSON(w, http.StatusOK, map[string]string{})
}

// GET /game-server/list  (내부 관리용)
func (h *Handler) ListGameServers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.registry.List())
}

// --- Matchmaking 핸들러 ---

// POST /match/enter
func (h *Handler) MatchEnter(w http.ResponseWriter, r *http.Request) {
	info, _, ok := h.requireSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or missing session token")
		return
	}

	var req struct {
		GameMode string `json:"game_mode"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck

	ticketID, err := h.queue.Enter(info.AccountID, req.GameMode)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ticket_id": ticketID})
}

// POST /match/cancel
func (h *Handler) MatchCancel(w http.ResponseWriter, r *http.Request) {
	info, _, ok := h.requireSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or missing session token")
		return
	}
	if err := h.queue.Cancel(info.AccountID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

// GET /match/status
func (h *Handler) MatchStatus(w http.ResponseWriter, r *http.Request) {
	info, _, ok := h.requireSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or missing session token")
		return
	}

	ticket, found := h.queue.GetTicket(info.AccountID)
	if !found {
		writeJSON(w, http.StatusOK, map[string]string{"status": "not_queued"})
		return
	}

	switch ticket.Status {
	case matchmaking.StatusMatched:
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "matched",
			"game_server": ticket.GameServer,
		})
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"status":         "queued",
			"queue_position": h.queue.QueueLength(),
		})
	}
}

// GET /health
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	servers := h.registry.List()
	healthy := 0
	for _, s := range servers {
		if s.IsHealthy() {
			healthy++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"active_sessions": h.sessions.Count(),
		"queue_length":    h.queue.QueueLength(),
		"game_servers":    len(servers),
		"healthy_servers": healthy,
	})
}
