package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// Config holds Redis connection configuration
type Config struct {
	Addr     string
	Password string
	DB       int
}

// Client wraps the go-redis client with logging and health checks
type Client struct {
	rdb    *redis.Client
	logger zerolog.Logger
}

// NewClient creates a new Redis client with the given configuration
func NewClient(cfg Config, logger zerolog.Logger) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	client := &Client{
		rdb:    rdb,
		logger: logger.With().Str("component", "redis").Logger(),
	}

	return client, nil
}

// Ping checks if Redis is reachable
func (c *Client) Ping(ctx context.Context) error {
	if err := c.rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	return nil
}

// Close closes the Redis connection
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Redis returns the underlying go-redis client for direct operations
func (c *Client) Redis() *redis.Client {
	return c.rdb
}

// Logger returns the client's logger
func (c *Client) Logger() zerolog.Logger {
	return c.logger
}
