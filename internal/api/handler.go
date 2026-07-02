package api

import (
	"encoding/json"
	"net/http"

	"github.com/thehun/session-server/internal/matchmaking"
	"github.com/thehun/session-server/internal/session"
)

// Handler는 모든 HTTP 핸들러를 묶는 구조체입니다.
type Handler struct {
	sessions *session.Store
	queue    *matchmaking.Queue
}

func NewHandler(sessions *session.Store, queue *matchmaking.Queue) *Handler {
	return &Handler{sessions: sessions, queue: queue}
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

// POST /auth/login
// Body: {"account_id":"alice","password":"pw"}
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

	// 실제 서비스에서는 DB/인증 서버 검증이 여기에 들어갑니다.
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

// --- Matchmaking 핸들러 ---

// POST /match/enter
// Header: X-Session-Token: <token>
// Body: {"game_mode":"ranked"} (선택)
// Response: {"ticket_id":"..."}
func (h *Handler) MatchEnter(w http.ResponseWriter, r *http.Request) {
	info, _, ok := h.requireSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or missing session token")
		return
	}

	var req struct {
		GameMode string `json:"game_mode"`
	}
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck — body is optional

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
// Response:
//   queued:   {"status":"queued",   "queue_position":N}
//   matched:  {"status":"matched",  "game_server":{"host":"...","port":7777}}
//   not_queued: {"status":"not_queued"}
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
