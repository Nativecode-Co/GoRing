package ws

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"

	"github.com/abdulkhalek/goring/internal/auth"
)

const (
	// Time allowed to write a message to the peer
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer
	maxMessageSize = 512 * 1024 // 512KB

	// Send channel buffer size
	sendBufferSize = 256
)

// MessageHandler is called for each message received from the client
type MessageHandler func(client *Client, message []byte)

// Client represents a WebSocket client connection
type Client struct {
	conn     *websocket.Conn
	userID   string
	userInfo *auth.UserInfo
	send     chan []byte
	done     chan struct{}
	once     sync.Once
	logger   zerolog.Logger
}

// NewClient creates a new WebSocket client
func NewClient(conn *websocket.Conn, userInfo *auth.UserInfo, logger zerolog.Logger) *Client {
	return &Client{
		conn:     conn,
		userID:   userInfo.UserID,
		userInfo: userInfo,
		send:     make(chan []byte, sendBufferSize),
		done:     make(chan struct{}),
		logger: logger.With().
			Str("component", "ws_client").
			Str("user_id", userInfo.UserID).
			Logger(),
	}
}

// UserID returns the client's user ID
func (c *Client) UserID() string {
	return c.userID
}

// UserInfo returns the client's user info
func (c *Client) UserInfo() *auth.UserInfo {
	return c.userInfo
}

// Send queues a message to be sent to the client.
// Returns false if the client is closed or the buffer is full.
func (c *Client) Send(message []byte) bool {
	select {
	case <-c.done:
		return false
	case c.send <- message:
		return true
	default:
		// Buffer full, drop message
		c.logger.Warn().
			Int("message_size", len(message)).
			Msg("Send buffer full, dropping message")
		return false
	}
}

// Close gracefully closes the client connection
func (c *Client) Close() {
	c.once.Do(func() {
		close(c.done)
		c.conn.Close()
		c.logger.Debug().Msg("Client connection closed")
	})
}

// Done returns a channel that's closed when the client is done
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// ReadPump reads messages from the WebSocket connection.
// Runs in its own goroutine. Calls handler for each message received.
// Exits when the connection is closed or an error occurs.
func (c *Client) ReadPump(ctx context.Context, handler MessageHandler) {
	defer func() {
		c.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		default:
		}

		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.logger.Warn().Err(err).Msg("WebSocket read error")
			}
			return
		}

		c.logger.Debug().
			Int("message_size", len(message)).
			Msg("Received message")

		handler(c, message)
	}
}

// WritePump writes messages to the WebSocket connection.
// Runs in its own goroutine. Sends messages from the send channel
// and periodic pings to keep the connection alive.
func (c *Client) WritePump(ctx context.Context) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			c.writeClose()
			return

		case <-c.done:
			return

		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Send channel was closed
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				c.logger.Warn().Err(err).Msg("WebSocket write error")
				return
			}

			c.logger.Debug().
				Int("message_size", len(message)).
				Msg("Sent message")

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.logger.Warn().Err(err).Msg("Ping write error")
				return
			}
		}
	}
}

// writeClose sends a close message to the client
func (c *Client) writeClose() {
	c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	c.conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
}
