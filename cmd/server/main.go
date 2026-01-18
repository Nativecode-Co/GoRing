package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/abdulkhalek/goring/internal/redis"
	"github.com/abdulkhalek/goring/internal/signaling"
	"github.com/abdulkhalek/goring/internal/ws"
)

func main() {
	// Initialize logger
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).
		With().
		Timestamp().
		Logger()

	// Load configuration from environment
	cfg := loadConfig()

	logger.Info().
		Str("port", cfg.Port).
		Str("redis_addr", cfg.RedisAddr).
		Str("server_id", cfg.ServerID).
		Msg("Starting WebSocket signaling server")

	// Initialize Redis client
	redisClient, err := redis.NewClient(redis.Config{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       0,
	}, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to create Redis client")
	}
	defer redisClient.Close()

	// Check Redis connection
	ctx := context.Background()
	if err := redisClient.Ping(ctx); err != nil {
		logger.Fatal().Err(err).Msg("Failed to connect to Redis")
	}
	logger.Info().Msg("Connected to Redis")

	// Initialize session manager
	sessions := redis.NewSessionManager(redisClient)

	// Initialize pub/sub
	pubsub := signaling.NewPubSub(redisClient, logger)
	defer pubsub.Close()

	// Initialize call manager
	callManager := signaling.NewCallManager(sessions, pubsub, logger)

	// Initialize WebSocket hub
	hub := ws.NewHub(sessions, callManager, pubsub, cfg.JWTSecret, cfg.ServerID, logger)

	// Start hub in background
	hubCtx, hubCancel := context.WithCancel(context.Background())
	go hub.Run(hubCtx)

	// Set up HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.HandleUpgrade)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if err := redisClient.Ping(r.Context()); err != nil {
			http.Error(w, "Redis unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in background
	go func() {
		logger.Info().Str("addr", server.Addr).Msg("HTTP server listening")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("HTTP server error")
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	logger.Info().Str("signal", sig.String()).Msg("Shutdown signal received")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Stop accepting new connections
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("HTTP server shutdown error")
	}

	// Stop hub (closes all WebSocket connections)
	hubCancel()
	hub.Stop()

	logger.Info().Msg("Server shutdown complete")
}

type config struct {
	Port          string
	JWTSecret     string
	RedisAddr     string
	RedisPassword string
	ServerID      string
}

func loadConfig() config {
	cfg := config{
		Port:          getEnv("PORT", "8080"),
		JWTSecret:     getEnv("JWT_SECRET", ""),
		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		ServerID:      getEnv("SERVER_ID", ""),
	}

	// Generate server ID from hostname if not set
	if cfg.ServerID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			cfg.ServerID = "server-1"
		} else {
			cfg.ServerID = hostname
		}
	}

	// Require JWT secret in production
	if cfg.JWTSecret == "" {
		// For development, use a default secret
		cfg.JWTSecret = "dev-secret-change-in-production"
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
