package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// Key prefixes
	keyPrefixWsUser      = "ws:user:"
	keyPrefixCallSession = "call:session:"
	keyPrefixCallUser    = "call:user:"

	// TTLs
	wsUserTTL      = 30 * time.Second
	callSessionTTL = 120 * time.Second
)

var (
	ErrSessionExists   = errors.New("session already exists")
	ErrSessionNotFound = errors.New("session not found")
	ErrUserBusy        = errors.New("user already in call")
	ErrStateConflict   = errors.New("state transition conflict")
)

// CallSession represents a call session stored in Redis
type CallSession struct {
	SessionID string
	CallerID  string
	CalleeID  string
	State     string
	CreatedAt time.Time
}

// SessionManager handles all session-related Redis operations
type SessionManager struct {
	client *Client
}

// NewSessionManager creates a new session manager
func NewSessionManager(client *Client) *SessionManager {
	return &SessionManager{client: client}
}

// WebSocket connection management

// AcquireWebSocket attempts to acquire a WebSocket slot for the user.
// Uses SET NX to ensure only one connection per user across all server instances.
// Returns true if acquired, false if user already has a connection.
func (m *SessionManager) AcquireWebSocket(ctx context.Context, userID, serverID string) (bool, error) {
	key := keyPrefixWsUser + userID

	// SET key value NX PX milliseconds
	// NX: Only set if key does not exist
	// PX: Set expiry in milliseconds
	result, err := m.client.rdb.SetNX(ctx, key, serverID, wsUserTTL).Result()
	if err != nil {
		return false, fmt.Errorf("acquire websocket: %w", err)
	}

	if result {
		m.client.logger.Debug().
			Str("user_id", userID).
			Str("server_id", serverID).
			Msg("WebSocket slot acquired")
	}

	return result, nil
}

// RefreshWebSocket refreshes the TTL for a user's WebSocket connection.
// Called periodically via ping/heartbeat to keep the connection alive.
func (m *SessionManager) RefreshWebSocket(ctx context.Context, userID string) error {
	key := keyPrefixWsUser + userID
	if err := m.client.rdb.Expire(ctx, key, wsUserTTL).Err(); err != nil {
		return fmt.Errorf("refresh websocket: %w", err)
	}
	return nil
}

// ReleaseWebSocket releases the WebSocket slot for the user.
// Called when the WebSocket connection is closed.
func (m *SessionManager) ReleaseWebSocket(ctx context.Context, userID string) error {
	key := keyPrefixWsUser + userID
	if err := m.client.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("release websocket: %w", err)
	}

	m.client.logger.Debug().
		Str("user_id", userID).
		Msg("WebSocket slot released")

	return nil
}

// IsUserOnline checks if a user has an active WebSocket connection
func (m *SessionManager) IsUserOnline(ctx context.Context, userID string) (bool, error) {
	key := keyPrefixWsUser + userID
	result, err := m.client.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("check user online: %w", err)
	}
	return result > 0, nil
}

// Call session management

// CreateCallSession creates a new call session in Redis.
// Uses a transaction to atomically check and set both user call mappings
// and the session hash to prevent race conditions.
func (m *SessionManager) CreateCallSession(ctx context.Context, session *CallSession) error {
	sessionKey := keyPrefixCallSession + session.SessionID
	callerUserKey := keyPrefixCallUser + session.CallerID
	calleeUserKey := keyPrefixCallUser + session.CalleeID

	// Use a transaction to atomically check both users are free and create session
	txf := func(tx *redis.Tx) error {
		// Check if either user is already in a call
		callerCall, err := tx.Get(ctx, callerUserKey).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return fmt.Errorf("check caller: %w", err)
		}
		if callerCall != "" {
			return ErrUserBusy
		}

		calleeCall, err := tx.Get(ctx, calleeUserKey).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return fmt.Errorf("check callee: %w", err)
		}
		if calleeCall != "" {
			return ErrUserBusy
		}

		// Check if session already exists
		exists, err := tx.Exists(ctx, sessionKey).Result()
		if err != nil {
			return fmt.Errorf("check session exists: %w", err)
		}
		if exists > 0 {
			return ErrSessionExists
		}

		// Execute all writes in a pipeline
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			// Create the session hash
			pipe.HSet(ctx, sessionKey, map[string]any{
				"caller_id":  session.CallerID,
				"callee_id":  session.CalleeID,
				"state":      session.State,
				"created_at": session.CreatedAt.Unix(),
			})
			pipe.Expire(ctx, sessionKey, callSessionTTL)

			// Map both users to this session
			pipe.Set(ctx, callerUserKey, session.SessionID, callSessionTTL)
			pipe.Set(ctx, calleeUserKey, session.SessionID, callSessionTTL)

			return nil
		})

		return err
	}

	// Execute the transaction with WATCH on user keys for optimistic locking
	err := m.client.rdb.Watch(ctx, txf, callerUserKey, calleeUserKey, sessionKey)
	if err != nil {
		return fmt.Errorf("create call session: %w", err)
	}

	m.client.logger.Info().
		Str("session_id", session.SessionID).
		Str("caller_id", session.CallerID).
		Str("callee_id", session.CalleeID).
		Str("state", session.State).
		Msg("Call session created")

	return nil
}

// GetCallSession retrieves a call session from Redis
func (m *SessionManager) GetCallSession(ctx context.Context, sessionID string) (*CallSession, error) {
	key := keyPrefixCallSession + sessionID

	result, err := m.client.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("get call session: %w", err)
	}

	if len(result) == 0 {
		return nil, ErrSessionNotFound
	}

	createdAt := int64(0)
	if v, ok := result["created_at"]; ok {
		fmt.Sscanf(v, "%d", &createdAt)
	}

	return &CallSession{
		SessionID: sessionID,
		CallerID:  result["caller_id"],
		CalleeID:  result["callee_id"],
		State:     result["state"],
		CreatedAt: time.Unix(createdAt, 0),
	}, nil
}

// Lua script for atomic state transition.
// Only updates state if current state matches expected state.
// Returns 1 if updated, 0 if state didn't match.
var stateTransitionScript = redis.NewScript(`
local current = redis.call('HGET', KEYS[1], 'state')
if current == ARGV[1] then
    redis.call('HSET', KEYS[1], 'state', ARGV[2])
    return 1
end
return 0
`)

// UpdateCallState atomically updates the call state if the current state matches.
// This prevents race conditions like double accept/reject.
func (m *SessionManager) UpdateCallState(ctx context.Context, sessionID, expectedState, newState string) (bool, error) {
	key := keyPrefixCallSession + sessionID

	result, err := stateTransitionScript.Run(ctx, m.client.rdb, []string{key}, expectedState, newState).Int()
	if err != nil {
		return false, fmt.Errorf("update call state: %w", err)
	}

	updated := result == 1
	if updated {
		m.client.logger.Info().
			Str("session_id", sessionID).
			Str("old_state", expectedState).
			Str("new_state", newState).
			Msg("Call state updated")
	} else {
		m.client.logger.Warn().
			Str("session_id", sessionID).
			Str("expected_state", expectedState).
			Str("new_state", newState).
			Msg("Call state transition rejected - state mismatch")
	}

	return updated, nil
}

// GetUserCall gets the session ID for a user's active call
func (m *SessionManager) GetUserCall(ctx context.Context, userID string) (string, error) {
	key := keyPrefixCallUser + userID
	result, err := m.client.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get user call: %w", err)
	}
	return result, nil
}

// ClearUserCall removes the user's call mapping
func (m *SessionManager) ClearUserCall(ctx context.Context, userID string) error {
	key := keyPrefixCallUser + userID
	if err := m.client.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("clear user call: %w", err)
	}
	return nil
}

// DeleteCallSession removes a call session and its user mappings
func (m *SessionManager) DeleteCallSession(ctx context.Context, sessionID string) error {
	// First get the session to know which users to clear
	session, err := m.GetCallSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil // Already deleted
		}
		return err
	}

	// Delete all related keys
	sessionKey := keyPrefixCallSession + sessionID
	callerUserKey := keyPrefixCallUser + session.CallerID
	calleeUserKey := keyPrefixCallUser + session.CalleeID

	if err := m.client.rdb.Del(ctx, sessionKey, callerUserKey, calleeUserKey).Err(); err != nil {
		return fmt.Errorf("delete call session: %w", err)
	}

	m.client.logger.Info().
		Str("session_id", sessionID).
		Msg("Call session deleted")

	return nil
}

// CleanupUserCall ends any active call for a user (used on disconnect)
func (m *SessionManager) CleanupUserCall(ctx context.Context, userID string) (*CallSession, error) {
	sessionID, err := m.GetUserCall(ctx, userID)
	if err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, nil // No active call
	}

	session, err := m.GetCallSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			// Session was already cleaned up, just clear user mapping
			_ = m.ClearUserCall(ctx, userID)
			return nil, nil
		}
		return nil, err
	}

	// Mark session as ended and delete
	_, _ = m.UpdateCallState(ctx, sessionID, session.State, "ended")
	if err := m.DeleteCallSession(ctx, sessionID); err != nil {
		return nil, err
	}

	return session, nil
}
