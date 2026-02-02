package ws

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"

	"github.com/abdulkhalek/goring/internal/auth"
	"github.com/abdulkhalek/goring/internal/protocol"
	"github.com/abdulkhalek/goring/internal/redis"
	"github.com/abdulkhalek/goring/internal/signaling"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// In production, implement proper origin checking
		return true
	},
}

// Hub manages all WebSocket connections and message routing
type Hub struct {
	clients     map[string]*Client // userID -> Client
	register    chan *Client
	unregister  chan *Client
	mu          sync.RWMutex
	sessions    *redis.SessionManager
	callManager *signaling.CallManager
	pubsub      *signaling.PubSub
	jwtSecret   string
	serverID    string
	logger      zerolog.Logger
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewHub creates a new Hub
func NewHub(
	sessions *redis.SessionManager,
	callManager *signaling.CallManager,
	pubsub *signaling.PubSub,
	jwtSecret string,
	serverID string,
	logger zerolog.Logger,
) *Hub {
	ctx, cancel := context.WithCancel(context.Background())

	hub := &Hub{
		clients:     make(map[string]*Client),
		register:    make(chan *Client),
		unregister:  make(chan *Client),
		sessions:    sessions,
		callManager: callManager,
		pubsub:      pubsub,
		jwtSecret:   jwtSecret,
		serverID:    serverID,
		logger:      logger.With().Str("component", "hub").Logger(),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Set the hub as the message sender for call manager
	callManager.SetSender(hub)

	return hub
}

// Run starts the hub's main loop
func (h *Hub) Run(ctx context.Context) {
	h.logger.Info().Msg("Hub started")

	for {
		select {
		case <-ctx.Done():
			h.shutdown()
			return

		case <-h.ctx.Done():
			h.shutdown()
			return

		case client := <-h.register:
			h.handleRegister(client)

		case client := <-h.unregister:
			h.handleUnregister(client)
		}
	}
}

// shutdown gracefully closes all connections
func (h *Hub) shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, client := range h.clients {
		client.Close()
	}
	h.clients = make(map[string]*Client)

	h.logger.Info().Msg("Hub shutdown complete")
}

// handleRegister adds a client to the hub
func (h *Hub) handleRegister(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Close any existing connection for this user
	if existing, ok := h.clients[client.userID]; ok {
		h.logger.Warn().
			Str("user_id", client.userID).
			Msg("Replacing existing connection")
		existing.Close()
	}

	h.clients[client.userID] = client

	// Subscribe to pub/sub for this user
	handler := func(userID string, message []byte) {
		h.handlePubSubMessage(userID, message)
	}
	if err := h.pubsub.Subscribe(h.ctx, client.userID, handler); err != nil {
		h.logger.Error().
			Str("user_id", client.userID).
			Err(err).
			Msg("Failed to subscribe to pub/sub")
	}

	h.logger.Info().
		Str("user_id", client.userID).
		Int("total_clients", len(h.clients)).
		Msg("Client registered")
}

// handleUnregister removes a client from the hub
func (h *Hub) handleUnregister(client *Client) {
	h.mu.Lock()
	existing, ok := h.clients[client.userID]
	if ok && existing == client {
		delete(h.clients, client.userID)
	}
	h.mu.Unlock()

	if !ok || existing != client {
		return // Client was already replaced
	}

	// Unsubscribe from pub/sub
	if err := h.pubsub.Unsubscribe(h.ctx, client.userID); err != nil {
		h.logger.Error().
			Str("user_id", client.userID).
			Err(err).
			Msg("Failed to unsubscribe from pub/sub")
	}

	// Release WebSocket slot in Redis
	if err := h.sessions.ReleaseWebSocket(h.ctx, client.userID); err != nil {
		h.logger.Error().
			Str("user_id", client.userID).
			Err(err).
			Msg("Failed to release WebSocket slot")
	}

	// Handle any active call
	if err := h.callManager.HandleDisconnect(h.ctx, client.userID); err != nil {
		h.logger.Error().
			Str("user_id", client.userID).
			Err(err).
			Msg("Failed to handle disconnect cleanup")
	}

	h.mu.RLock()
	totalClients := len(h.clients)
	h.mu.RUnlock()

	h.logger.Info().
		Str("user_id", client.userID).
		Int("total_clients", totalClients).
		Msg("Client unregistered")
}

// handlePubSubMessage processes messages received via Redis pub/sub
func (h *Hub) handlePubSubMessage(userID string, message []byte) {
	// Check if this is an internal disconnect message
	msg, err := protocol.ParseMessage(message)
	if err == nil && msg.Type == protocol.TypeDisconnect {
		h.logger.Info().
			Str("user_id", userID).
			Msg("Received disconnect message via pub/sub")

		h.mu.RLock()
		client, ok := h.clients[userID]
		h.mu.RUnlock()

		if ok {
			client.Close()
		}
		return
	}

	h.mu.RLock()
	client, ok := h.clients[userID]
	h.mu.RUnlock()

	if !ok {
		h.logger.Warn().
			Str("user_id", userID).
			Msg("Received pub/sub message for disconnected user")
		return
	}

	if !client.Send(message) {
		h.logger.Warn().
			Str("user_id", userID).
			Msg("Failed to forward pub/sub message")
	}
}

// SendToUser sends a message to a user.
// If the user is connected locally, sends directly.
// Otherwise, publishes to Redis pub/sub for other instances.
func (h *Hub) SendToUser(ctx context.Context, userID string, msg *protocol.Message) error {
	data, err := msg.Bytes()
	if err != nil {
		return err
	}

	h.mu.RLock()
	client, ok := h.clients[userID]
	h.mu.RUnlock()

	if ok {
		// User is connected to this instance
		if client.Send(data) {
			return nil
		}
		// Send failed, try pub/sub as fallback
	}

	// User is not connected locally (or send failed), use pub/sub
	return h.pubsub.Publish(ctx, userID, data)
}

// HandleUpgrade handles WebSocket upgrade requests
func (h *Hub) HandleUpgrade(w http.ResponseWriter, r *http.Request) {
	// Extract token from query parameter
	token := r.URL.Query().Get("token")
	if token == "" {
		h.logger.Warn().Msg("WebSocket upgrade: missing token")
		http.Error(w, "Missing token", http.StatusUnauthorized)
		return
	}

	// Validate JWT
	userInfo, err := auth.ValidateToken(token, h.jwtSecret)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Msg("WebSocket upgrade: invalid token")
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	// Try to acquire WebSocket slot in Redis (ensures single connection per user)
	acquired, err := h.sessions.AcquireWebSocket(r.Context(), userInfo.UserID, h.serverID)
	if err != nil {
		h.logger.Error().
			Str("user_id", userInfo.UserID).
			Err(err).
			Msg("WebSocket upgrade: Redis error")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if !acquired {
		// User already has a connection - kick existing and take over
		if err := h.kickExistingConnection(r.Context(), userInfo.UserID); err != nil {
			h.logger.Error().
				Str("user_id", userInfo.UserID).
				Err(err).
				Msg("WebSocket upgrade: failed to kick existing connection")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Force acquire the slot
		if err := h.sessions.ForceAcquireWebSocket(r.Context(), userInfo.UserID, h.serverID); err != nil {
			h.logger.Error().
				Str("user_id", userInfo.UserID).
				Err(err).
				Msg("WebSocket upgrade: failed to force acquire slot")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	// Upgrade connection
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error().
			Str("user_id", userInfo.UserID).
			Err(err).
			Msg("WebSocket upgrade: upgrade failed")
		// Release the slot we acquired
		_ = h.sessions.ReleaseWebSocket(r.Context(), userInfo.UserID)
		return
	}

	// Create client
	client := NewClient(conn, userInfo, h.logger)

	// Register client
	h.register <- client

	// Start read/write pumps
	go h.runClient(client)

	h.logger.Info().
		Str("user_id", userInfo.UserID).
		Str("remote_addr", r.RemoteAddr).
		Msg("WebSocket connection established")
}

// kickExistingConnection disconnects an existing user connection.
// If the user is connected to this server, closes directly.
// If connected to another server, sends disconnect message via pub/sub.
func (h *Hub) kickExistingConnection(ctx context.Context, userID string) error {
	// Check which server owns the connection
	existingServer, err := h.sessions.GetWebSocketServer(ctx, userID)
	if err != nil {
		return err
	}

	if existingServer == "" {
		// No existing connection, nothing to kick
		return nil
	}

	h.logger.Info().
		Str("user_id", userID).
		Str("existing_server", existingServer).
		Str("this_server", h.serverID).
		Msg("Kicking existing connection")

	if existingServer == h.serverID {
		// User is connected to this server - close directly
		h.mu.RLock()
		client, ok := h.clients[userID]
		h.mu.RUnlock()

		if ok {
			client.Close()
		}
	} else {
		// User is connected to another server - send disconnect via pub/sub
		disconnectMsg := protocol.MustNewMessage(protocol.TypeDisconnect, protocol.DisconnectPayload{
			Reason: "new_connection",
		})
		data, err := disconnectMsg.Bytes()
		if err != nil {
			return err
		}
		if err := h.pubsub.Publish(ctx, userID, data); err != nil {
			return err
		}
	}

	// Give the other server a moment to process the disconnect
	time.Sleep(100 * time.Millisecond)

	return nil
}

// runClient manages a client's lifecycle
func (h *Hub) runClient(client *Client) {
	ctx := h.ctx

	// Start write pump in background
	go client.WritePump(ctx)

	// Start heartbeat TTL refresh in background
	go h.heartbeatLoop(ctx, client)

	// Read pump runs in this goroutine (blocks until client disconnects)
	client.ReadPump(ctx, h.handleMessage)

	// Client disconnected, unregister
	h.unregister <- client
}

// heartbeatLoop refreshes the Redis TTL for the user's WebSocket slot
func (h *Hub) heartbeatLoop(ctx context.Context, client *Client) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-client.Done():
			return
		case <-ticker.C:
			if err := h.sessions.RefreshWebSocket(ctx, client.userID); err != nil {
				h.logger.Warn().
					Str("user_id", client.userID).
					Err(err).
					Msg("Failed to refresh WebSocket TTL")
			}
		}
	}
}

// handleMessage processes incoming WebSocket messages
func (h *Hub) handleMessage(client *Client, data []byte) {
	msg, err := protocol.ParseMessage(data)
	if err != nil {
		h.logger.Warn().
			Str("user_id", client.userID).
			Err(err).
			Msg("Failed to parse message")
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Invalid message format")
		return
	}

	h.logger.Debug().
		Str("user_id", client.userID).
		Str("type", msg.Type).
		Msg("Processing message")

	ctx := h.ctx

	switch msg.Type {
	case protocol.TypeCallStart:
		h.handleCallStart(ctx, client, msg)
	case protocol.TypeCallAccept:
		h.handleCallAccept(ctx, client, msg)
	case protocol.TypeCallReject:
		h.handleCallReject(ctx, client, msg)
	case protocol.TypeCallEnd:
		h.handleCallEnd(ctx, client, msg)
	case protocol.TypeWebRTCOffer:
		h.handleWebRTCOffer(ctx, client, msg)
	case protocol.TypeWebRTCAnswer:
		h.handleWebRTCAnswer(ctx, client, msg)
	case protocol.TypeWebRTCICE:
		h.handleWebRTCICE(ctx, client, msg)
	default:
		h.logger.Warn().
			Str("user_id", client.userID).
			Str("type", msg.Type).
			Msg("Unknown message type")
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Unknown message type")
	}
}

// handleCallStart handles call.start messages
func (h *Hub) handleCallStart(ctx context.Context, client *Client, msg *protocol.Message) {
	var payload protocol.CallStartPayload
	if err := msg.ParsePayload(&payload); err != nil {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Invalid payload")
		return
	}

	if payload.CalleeID == "" {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Missing callee_id")
		return
	}

	if payload.CalleeID == client.userID {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Cannot call yourself")
		return
	}

	callerInfo := toProtocolUserInfo(client.UserInfo())
	session, err := h.callManager.StartCall(ctx, client.userID, payload.CalleeID, callerInfo)
	if err != nil {
		switch err {
		case signaling.ErrUserOffline:
			h.sendError(client, protocol.ErrCodeUserOffline, "User is offline")
		case signaling.ErrUserBusy:
			h.sendError(client, protocol.ErrCodeUserBusy, "User is busy")
		default:
			h.logger.Error().
				Str("user_id", client.userID).
				Err(err).
				Msg("Failed to start call")
			h.sendError(client, protocol.ErrCodeInternalError, "Failed to start call")
		}
		return
	}

	// Send ringing confirmation to caller with session info
	ringingMsg := protocol.MustNewMessage(protocol.TypeCallRinging, protocol.CallRingingPayload{
		SessionID: session.SessionID,
		CalleeID:  payload.CalleeID,
	})
	data, err := ringingMsg.Bytes()
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to serialize ringing message")
		return
	}
	client.Send(data)
}

// handleCallAccept handles call.accept messages
func (h *Hub) handleCallAccept(ctx context.Context, client *Client, msg *protocol.Message) {
	var payload protocol.CallSessionPayload
	if err := msg.ParsePayload(&payload); err != nil {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Invalid payload")
		return
	}

	if payload.SessionID == "" {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Missing session_id")
		return
	}

	calleeInfo := toProtocolUserInfo(client.UserInfo())
	err := h.callManager.AcceptCall(ctx, client.userID, payload.SessionID, calleeInfo)
	if err != nil {
		switch err {
		case signaling.ErrSessionNotFound:
			h.sendError(client, protocol.ErrCodeSessionNotFound, "Session not found")
		case signaling.ErrNotAuthorized:
			h.sendError(client, protocol.ErrCodeUnauthorized, "Not authorized")
		case signaling.ErrInvalidState:
			h.sendError(client, protocol.ErrCodeInvalidState, "Invalid state transition")
		default:
			h.logger.Error().
				Str("user_id", client.userID).
				Str("session_id", payload.SessionID).
				Err(err).
				Msg("Failed to accept call")
			h.sendError(client, protocol.ErrCodeInternalError, "Failed to accept call")
		}
		return
	}
}

// handleCallReject handles call.reject messages
func (h *Hub) handleCallReject(ctx context.Context, client *Client, msg *protocol.Message) {
	var payload protocol.CallSessionPayload
	if err := msg.ParsePayload(&payload); err != nil {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Invalid payload")
		return
	}

	if payload.SessionID == "" {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Missing session_id")
		return
	}

	calleeInfo := toProtocolUserInfo(client.UserInfo())
	err := h.callManager.RejectCall(ctx, client.userID, payload.SessionID, calleeInfo)
	if err != nil {
		switch err {
		case signaling.ErrSessionNotFound:
			h.sendError(client, protocol.ErrCodeSessionNotFound, "Session not found")
		case signaling.ErrNotAuthorized:
			h.sendError(client, protocol.ErrCodeUnauthorized, "Not authorized")
		case signaling.ErrInvalidState:
			h.sendError(client, protocol.ErrCodeInvalidState, "Invalid state transition")
		default:
			h.logger.Error().
				Str("user_id", client.userID).
				Str("session_id", payload.SessionID).
				Err(err).
				Msg("Failed to reject call")
			h.sendError(client, protocol.ErrCodeInternalError, "Failed to reject call")
		}
		return
	}
}

// handleCallEnd handles call.end messages
func (h *Hub) handleCallEnd(ctx context.Context, client *Client, msg *protocol.Message) {
	var payload protocol.CallSessionPayload
	if err := msg.ParsePayload(&payload); err != nil {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Invalid payload")
		return
	}

	if payload.SessionID == "" {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Missing session_id")
		return
	}

	userInfo := toProtocolUserInfo(client.UserInfo())
	err := h.callManager.EndCall(ctx, client.userID, payload.SessionID, userInfo)
	if err != nil {
		switch err {
		case signaling.ErrSessionNotFound:
			h.sendError(client, protocol.ErrCodeSessionNotFound, "Session not found")
		case signaling.ErrNotAuthorized:
			h.sendError(client, protocol.ErrCodeUnauthorized, "Not authorized")
		default:
			h.logger.Error().
				Str("user_id", client.userID).
				Str("session_id", payload.SessionID).
				Err(err).
				Msg("Failed to end call")
			h.sendError(client, protocol.ErrCodeInternalError, "Failed to end call")
		}
		return
	}
}

// handleWebRTCOffer handles webrtc.offer messages
func (h *Hub) handleWebRTCOffer(ctx context.Context, client *Client, msg *protocol.Message) {
	var payload protocol.WebRTCOfferPayload
	if err := msg.ParsePayload(&payload); err != nil {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Invalid payload")
		return
	}

	if payload.SessionID == "" || payload.SDP == "" {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Missing session_id or sdp")
		return
	}

	err := h.callManager.ForwardWebRTC(ctx, client.userID, payload.SessionID, msg)
	if err != nil {
		switch err {
		case signaling.ErrSessionNotFound:
			h.sendError(client, protocol.ErrCodeSessionNotFound, "Session not found")
		case signaling.ErrNotAuthorized:
			h.sendError(client, protocol.ErrCodeUnauthorized, "Not authorized")
		case signaling.ErrInvalidState:
			h.sendError(client, protocol.ErrCodeInvalidState, "Call not in accepted state")
		default:
			h.sendError(client, protocol.ErrCodeInternalError, "Failed to forward offer")
		}
	}
}

// handleWebRTCAnswer handles webrtc.answer messages
func (h *Hub) handleWebRTCAnswer(ctx context.Context, client *Client, msg *protocol.Message) {
	var payload protocol.WebRTCAnswerPayload
	if err := msg.ParsePayload(&payload); err != nil {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Invalid payload")
		return
	}

	if payload.SessionID == "" || payload.SDP == "" {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Missing session_id or sdp")
		return
	}

	err := h.callManager.ForwardWebRTC(ctx, client.userID, payload.SessionID, msg)
	if err != nil {
		switch err {
		case signaling.ErrSessionNotFound:
			h.sendError(client, protocol.ErrCodeSessionNotFound, "Session not found")
		case signaling.ErrNotAuthorized:
			h.sendError(client, protocol.ErrCodeUnauthorized, "Not authorized")
		case signaling.ErrInvalidState:
			h.sendError(client, protocol.ErrCodeInvalidState, "Call not in accepted state")
		default:
			h.sendError(client, protocol.ErrCodeInternalError, "Failed to forward answer")
		}
	}
}

// handleWebRTCICE handles webrtc.ice messages
func (h *Hub) handleWebRTCICE(ctx context.Context, client *Client, msg *protocol.Message) {
	var payload protocol.WebRTCICEPayload
	if err := msg.ParsePayload(&payload); err != nil {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Invalid payload")
		return
	}

	if payload.SessionID == "" {
		h.sendError(client, protocol.ErrCodeInvalidMessage, "Missing session_id")
		return
	}

	err := h.callManager.ForwardWebRTC(ctx, client.userID, payload.SessionID, msg)
	if err != nil {
		switch err {
		case signaling.ErrSessionNotFound:
			h.sendError(client, protocol.ErrCodeSessionNotFound, "Session not found")
		case signaling.ErrNotAuthorized:
			h.sendError(client, protocol.ErrCodeUnauthorized, "Not authorized")
		case signaling.ErrInvalidState:
			h.sendError(client, protocol.ErrCodeInvalidState, "Call not in accepted state")
		default:
			h.sendError(client, protocol.ErrCodeInternalError, "Failed to forward ICE candidate")
		}
	}
}

// sendError sends an error message to a client
func (h *Hub) sendError(client *Client, code, message string) {
	errMsg := protocol.NewErrorMessage(code, message)
	data, err := errMsg.Bytes()
	if err != nil {
		h.logger.Error().Err(err).Msg("Failed to serialize error message")
		return
	}
	client.Send(data)
}

// toProtocolUserInfo converts auth.UserInfo to protocol.UserInfo
func toProtocolUserInfo(info *auth.UserInfo) *protocol.UserInfo {
	if info == nil {
		return nil
	}
	return &protocol.UserInfo{
		UserID:       info.UserID,
		Name:         info.Name,
		Username:     info.Username,
		ImageProfile: info.ImageProfile,
	}
}

// Stop stops the hub
func (h *Hub) Stop() {
	h.cancel()
}
