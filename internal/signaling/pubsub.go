package signaling

import (
	"context"
	"fmt"
	"sync"

	goredis "github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/abdulkhalek/goring/internal/redis"
)

const (
	channelPrefix = "ws:signal:"
)

// MessageHandler is called when a message is received for a user
type MessageHandler func(userID string, message []byte)

// PubSub manages Redis pub/sub for cross-instance message delivery.
// When a message needs to be sent to a user connected to a different
// server instance, it's published to their channel and the instance
// with the connection forwards it to their WebSocket.
type PubSub struct {
	client        *redis.Client
	logger        zerolog.Logger
	subscriptions map[string]*goredis.PubSub
	handlers      map[string]MessageHandler
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewPubSub creates a new pub/sub manager
func NewPubSub(client *redis.Client, logger zerolog.Logger) *PubSub {
	ctx, cancel := context.WithCancel(context.Background())
	return &PubSub{
		client:        client,
		logger:        logger.With().Str("component", "pubsub").Logger(),
		subscriptions: make(map[string]*goredis.PubSub),
		handlers:      make(map[string]MessageHandler),
		ctx:           ctx,
		cancel:        cancel,
	}
}

// Subscribe subscribes to messages for a specific user.
// The handler is called when a message is published to the user's channel.
func (p *PubSub) Subscribe(ctx context.Context, userID string, handler MessageHandler) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if already subscribed
	if _, exists := p.subscriptions[userID]; exists {
		p.handlers[userID] = handler // Update handler
		return nil
	}

	channel := channelPrefix + userID
	sub := p.client.Redis().Subscribe(ctx, channel)

	// Wait for subscription confirmation
	_, err := sub.Receive(ctx)
	if err != nil {
		sub.Close()
		return fmt.Errorf("subscribe to %s: %w", channel, err)
	}

	p.subscriptions[userID] = sub
	p.handlers[userID] = handler

	// Start goroutine to receive messages
	go p.receiveMessages(userID, sub)

	p.logger.Debug().
		Str("user_id", userID).
		Str("channel", channel).
		Msg("Subscribed to user channel")

	return nil
}

// receiveMessages processes incoming messages for a user subscription
func (p *PubSub) receiveMessages(userID string, sub *goredis.PubSub) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error().
				Interface("panic", r).
				Str("user_id", userID).
				Msg("Panic in PubSub receiveMessages")
		}
	}()
	ch := sub.Channel()

	for {
		select {
		case <-p.ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return // Channel closed
			}

			p.mu.RLock()
			handler, exists := p.handlers[userID]
			p.mu.RUnlock()

			if exists && handler != nil {
				p.logger.Debug().
					Str("user_id", userID).
					Int("message_size", len(msg.Payload)).
					Msg("Received pub/sub message")

				handler(userID, []byte(msg.Payload))
			}
		}
	}
}

// Unsubscribe removes subscription for a user
func (p *PubSub) Unsubscribe(ctx context.Context, userID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	sub, exists := p.subscriptions[userID]
	if !exists {
		return nil
	}

	if err := sub.Close(); err != nil {
		p.logger.Warn().
			Str("user_id", userID).
			Err(err).
			Msg("Error closing subscription")
	}

	delete(p.subscriptions, userID)
	delete(p.handlers, userID)

	p.logger.Debug().
		Str("user_id", userID).
		Msg("Unsubscribed from user channel")

	return nil
}

// Publish sends a message to a user's channel.
// This is used when the target user is connected to a different server instance.
func (p *PubSub) Publish(ctx context.Context, userID string, message []byte) error {
	channel := channelPrefix + userID

	result, err := p.client.Redis().Publish(ctx, channel, message).Result()
	if err != nil {
		return fmt.Errorf("publish to %s: %w", channel, err)
	}

	p.logger.Debug().
		Str("user_id", userID).
		Str("channel", channel).
		Int64("receivers", result).
		Int("message_size", len(message)).
		Msg("Published message to channel")

	return nil
}

// Close closes all subscriptions and stops the pub/sub manager
func (p *PubSub) Close() error {
	p.cancel()

	p.mu.Lock()
	defer p.mu.Unlock()

	for userID, sub := range p.subscriptions {
		if err := sub.Close(); err != nil {
			p.logger.Warn().
				Str("user_id", userID).
				Err(err).
				Msg("Error closing subscription during shutdown")
		}
	}

	p.subscriptions = make(map[string]*goredis.PubSub)
	p.handlers = make(map[string]MessageHandler)

	p.logger.Info().Msg("PubSub manager closed")
	return nil
}
