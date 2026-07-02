package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/thehun/session-server/internal/api"
	"github.com/thehun/session-server/internal/matchmaking"
	"github.com/thehun/session-server/internal/session"
)

// Config는 환경 변수로 오버라이드 가능한 서버 설정입니다.
type Config struct {
	HTTPPort        int
	GameServerHost  string
	GameServerPort  int
	PlayersPerMatch int
}

func loadConfig() Config {
	cfg := Config{
		HTTPPort:        8080,
		GameServerHost:  "127.0.0.1",
		GameServerPort:  7777,
		PlayersPerMatch: 2,
	}

	if v := os.Getenv("HTTP_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.HTTPPort = n
		}
	}
	if v := os.Getenv("GAME_SERVER_HOST"); v != "" {
		cfg.GameServerHost = v
	}
	if v := os.Getenv("GAME_SERVER_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.GameServerPort = n
		}
	}
	if v := os.Getenv("PLAYERS_PER_MATCH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			cfg.PlayersPerMatch = n
		}
	}

	return cfg
}

func main() {
	cfg := loadConfig()

	sessionStore := session.NewStore()
	matchQueue := matchmaking.NewQueue(
		cfg.PlayersPerMatch,
		cfg.GameServerHost,
		cfg.GameServerPort,
	)

	// 만료된 매칭 티켓 주기적으로 정리
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			matchQueue.CleanupExpired()
		}
	}()

	handler := api.NewHandler(sessionStore, matchQueue)
	router := api.NewRouter(handler)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("session-server listening on :%d", cfg.HTTPPort)
		log.Printf("game server: %s:%d  players/match: %d",
			cfg.GameServerHost, cfg.GameServerPort, cfg.PlayersPerMatch)

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe error: %v", err)
		}
	}()

	<-quit
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("server shutdown error: %v", err)
	}

	log.Println("session-server stopped")
}
