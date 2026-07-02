package api

import (
	"net/http"
)

func NewRouter(h *Handler) http.Handler {
	mux := http.NewServeMux()

	// Auth
	mux.HandleFunc("/auth/register",  methodOnly(http.MethodPost, h.Register))
	mux.HandleFunc("/auth/login",     methodOnly(http.MethodPost, h.Login))
	mux.HandleFunc("/auth/refresh",   methodOnly(http.MethodPost, h.Refresh))
	mux.HandleFunc("/auth/logout",    methodOnly(http.MethodPost, h.Logout))
	mux.HandleFunc("/auth/me",        methodOnly(http.MethodGet,  h.Me))
	mux.HandleFunc("/auth/validate",  methodOnly(http.MethodGet,  h.ValidateToken))

	// Player
	mux.HandleFunc("/player/stats",   methodOnly(http.MethodGet,  h.PlayerStats))
	mux.HandleFunc("/player/result",  methodOnly(http.MethodPost, h.PlayerResult))

	// Matchmaking
	mux.HandleFunc("/match/enter",    methodOnly(http.MethodPost, h.MatchEnter))
	mux.HandleFunc("/match/cancel",   methodOnly(http.MethodPost, h.MatchCancel))
	mux.HandleFunc("/match/status",   methodOnly(http.MethodGet,  h.MatchStatus))

	// Game Server Registry (리얼타임서버 → session-server 내부 호출)
	mux.HandleFunc("/game-server/register",   methodOnly(http.MethodPost, h.RegisterGameServer))
	mux.HandleFunc("/game-server/heartbeat",  methodOnly(http.MethodPost, h.GameServerHeartbeat))
	mux.HandleFunc("/game-server/unregister", methodOnly(http.MethodPost, h.UnregisterGameServer))
	mux.HandleFunc("/game-server/list",       methodOnly(http.MethodGet,  h.ListGameServers))

	mux.HandleFunc("/health", h.Health)

	return mux
}

func methodOnly(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}
