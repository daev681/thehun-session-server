package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/thehun/session-server/internal/account"
	"github.com/thehun/session-server/internal/matchmaking"
	"github.com/thehun/session-server/internal/session"
)

type Handler struct {
	sessions *session.Store
	queue    *matchmaking.Queue
	accounts *account.Repo
}

func NewHandler(sessions *session.Store, queue *matchmaking.Queue, accounts *account.Repo) *Handler {
	return &Handler{sessions: sessions, queue: queue, accounts: accounts}
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

// --- Auth 핸들러 ---

// POST /auth/register
// Body: {"account_id":"alice","password":"pw123","display_name":"Alice"}
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
// Body: {"account_id":"alice","password":"pw123"}
// Response: {"session_token":"...","account_id":"alice"}
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
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

// POST /auth/logout
// Header: X-Session-Token: <token>
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
// Header: X-Session-Token: <token>
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	info, _, ok := h.requireSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or missing session token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"account_id": info.AccountID})
}

// GET /auth/validate?token=<token>
// C++ 리얼타임서버가 클라이언트 토큰을 검증할 때 호출합니다.
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
// Header: X-Session-Token: <token>
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
// 리얼타임서버가 게임 종료 후 호출 (승/패 기록)
// Body: {"winner":"accountId","losers":["accountId1",...]}
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

// --- Matchmaking 핸들러 ---

// POST /match/enter
// Header: X-Session-Token: <token>
// Body: {"game_mode":"ranked"} (선택)
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
// Header: X-Session-Token: <token>
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
// Header: X-Session-Token: <token>
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
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"active_sessions": h.sessions.Count(),
		"queue_length":    h.queue.QueueLength(),
	})
}
