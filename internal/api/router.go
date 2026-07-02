package api

import (
	"net/http"
)

// NewRouter는 표준 net/http ServeMux로 라우팅을 구성합니다.
// 외부 라우터 의존성 없이 메서드 분기를 직접 처리합니다.
func NewRouter(h *Handler) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/auth/login",    methodOnly(http.MethodPost, h.Login))
	mux.HandleFunc("/auth/logout",   methodOnly(http.MethodPost, h.Logout))
	mux.HandleFunc("/auth/me",       methodOnly(http.MethodGet,  h.Me))
	mux.HandleFunc("/auth/validate", methodOnly(http.MethodGet,  h.ValidateToken))

	mux.HandleFunc("/match/enter",   methodOnly(http.MethodPost, h.MatchEnter))
	mux.HandleFunc("/match/cancel",  methodOnly(http.MethodPost, h.MatchCancel))
	mux.HandleFunc("/match/status",  methodOnly(http.MethodGet,  h.MatchStatus))

	mux.HandleFunc("/health", h.Health)

	return mux
}

// methodOnly는 지정한 HTTP 메서드만 허용하는 미들웨어입니다.
func methodOnly(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}
