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

	"github.com/thehun/session-server/internal/account"
	"github.com/thehun/session-server/internal/api"
	"github.com/thehun/session-server/internal/db"
	"github.com/thehun/session-server/internal/matchmaking"
	"github.com/thehun/session-server/internal/session"
)

type Config struct {
	HTTPPort        int
	DatabaseURL     string
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
	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
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

	ctx := context.Background()

	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("db migrate: %v", err)
	}
	log.Println("database connected and migrated")

	accountRepo  := account.NewRepo(pool)
	sessionStore := session.NewStore()
	matchQueue   := matchmaking.NewQueue(
		cfg.PlayersPerMatch,
		cfg.GameServerHost,
		cfg.GameServerPort,
	)

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			matchQueue.CleanupExpired()
		}
	}()

	handler := api.NewHandler(sessionStore, matchQueue, accountRepo)
	router  := api.NewRouter(handler)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("server shutdown error: %v", err)
	}

	log.Println("session-server stopped")
}
