package signaling

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/abdulkhalek/goring/internal/protocol"
	"github.com/abdulkhalek/goring/internal/redis"
)

var (
	ErrUserBusy        = errors.New("user is busy")
	ErrUserOffline     = errors.New("user is offline")
	ErrSessionNotFound = errors.New("session not found")
	ErrNotAuthorized   = errors.New("not authorized for this session")
	ErrInvalidState    = errors.New("invalid state transition")
)

// MessageSender is used by CallManager to send messages to users
type MessageSender interface {
	SendToUser(ctx context.Context, userID string, msg *protocol.Message) error
}

// CallManager handles call signaling logic and state management
type CallManager struct {
	sessions *redis.SessionManager
	pubsub   *PubSub
	logger   zerolog.Logger
	sender   MessageSender
}

// NewCallManager creates a new call manager
func NewCallManager(sessions *redis.SessionManager, pubsub *PubSub, logger zerolog.Logger) *CallManager {
	return &CallManager{
		sessions: sessions,
		pubsub:   pubsub,
		logger:   logger.With().Str("component", "call_manager").Logger(),
	}
}

// SetSender sets the message sender (typically the Hub)
// This is set after Hub creation to avoid circular dependency
func (m *CallManager) SetSender(sender MessageSender) {
	m.sender = sender
}

// StartCall initiates a new call from caller to callee.
// Creates a new session in Redis and sends ring notification to callee.
func (m *CallManager) StartCall(ctx context.Context, callerID, calleeID string, callerInfo *protocol.UserInfo) (*redis.CallSession, error) {
	m.logger.Info().
		Str("caller_id", callerID).
		Str("callee_id", calleeID).
		Msg("Starting call")

	// Check if callee is online
	online, err := m.sessions.IsUserOnline(ctx, calleeID)
	if err != nil {
		return nil, err
	}
	if !online {
		m.logger.Warn().
			Str("caller_id", callerID).
			Str("callee_id", calleeID).
			Msg("Call failed: callee offline")
		return nil, ErrUserOffline
	}

	// Create session
	session := &redis.CallSession{
		SessionID: uuid.New().String(),
		CallerID:  callerID,
		CalleeID:  calleeID,
		State:     protocol.StateRinging,
		CreatedAt: time.Now(),
	}

	// CreateCallSession will atomically check if either user is busy
	if err := m.sessions.CreateCallSession(ctx, session); err != nil {
		if errors.Is(err, redis.ErrUserBusy) {
			m.logger.Warn().
				Str("caller_id", callerID).
				Str("callee_id", calleeID).
				Msg("Call failed: user busy")
			return nil, ErrUserBusy
		}
		return nil, err
	}

	// Send ring notification to callee
	ringMsg := protocol.MustNewMessage(protocol.TypeCallRing, protocol.CallRingPayload{
		SessionID:  session.SessionID,
		CallerID:   callerID,
		CallerInfo: callerInfo,
	})

	if m.sender != nil {
		if err := m.sender.SendToUser(ctx, calleeID, ringMsg); err != nil {
			m.logger.Error().
				Str("session_id", session.SessionID).
				Str("callee_id", calleeID).
				Err(err).
				Msg("Failed to send ring notification")
			// Don't fail the call - callee might receive via pubsub or reconnect
		}
	}

	m.logger.Info().
		Str("session_id", session.SessionID).
		Str("caller_id", callerID).
		Str("callee_id", calleeID).
		Msg("Call started, ringing callee")

	return session, nil
}

// AcceptCall accepts an incoming call.
// Validates the callee owns the session and atomically transitions state.
func (m *CallManager) AcceptCall(ctx context.Context, userID, sessionID string, calleeInfo *protocol.UserInfo) error {
	m.logger.Info().
		Str("user_id", userID).
		Str("session_id", sessionID).
		Msg("Accepting call")

	session, err := m.sessions.GetCallSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, redis.ErrSessionNotFound) {
			return ErrSessionNotFound
		}
		return err
	}

	// Only callee can accept
	if session.CalleeID != userID {
		m.logger.Warn().
			Str("user_id", userID).
			Str("session_id", sessionID).
			Str("callee_id", session.CalleeID).
			Msg("Accept rejected: not the callee")
		return ErrNotAuthorized
	}

	// Atomic state transition: ringing -> accepted
	updated, err := m.sessions.UpdateCallState(ctx, sessionID, protocol.StateRinging, protocol.StateAccepted)
	if err != nil {
		return err
	}
	if !updated {
		m.logger.Warn().
			Str("session_id", sessionID).
			Str("current_state", session.State).
			Msg("Accept rejected: state conflict")
		return ErrInvalidState
	}

	// Notify caller that call was accepted
	acceptedMsg := protocol.MustNewMessage(protocol.TypeCallAccepted, protocol.CallAcceptedPayload{
		SessionID:  sessionID,
		CalleeInfo: calleeInfo,
	})

	if m.sender != nil {
		if err := m.sender.SendToUser(ctx, session.CallerID, acceptedMsg); err != nil {
			m.logger.Error().
				Str("session_id", sessionID).
				Str("caller_id", session.CallerID).
				Err(err).
				Msg("Failed to notify caller of acceptance")
		}
	}

	m.logger.Info().
		Str("session_id", sessionID).
		Str("caller_id", session.CallerID).
		Str("callee_id", session.CalleeID).
		Msg("Call accepted")

	return nil
}

// RejectCall rejects an incoming call.
// Validates the callee owns the session and cleans up.
func (m *CallManager) RejectCall(ctx context.Context, userID, sessionID string, calleeInfo *protocol.UserInfo) error {
	m.logger.Info().
		Str("user_id", userID).
		Str("session_id", sessionID).
		Msg("Rejecting call")

	session, err := m.sessions.GetCallSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, redis.ErrSessionNotFound) {
			return ErrSessionNotFound
		}
		return err
	}

	// Only callee can reject
	if session.CalleeID != userID {
		m.logger.Warn().
			Str("user_id", userID).
			Str("session_id", sessionID).
			Str("callee_id", session.CalleeID).
			Msg("Reject rejected: not the callee")
		return ErrNotAuthorized
	}

	// Atomic state transition: ringing -> rejected
	updated, err := m.sessions.UpdateCallState(ctx, sessionID, protocol.StateRinging, protocol.StateRejected)
	if err != nil {
		return err
	}
	if !updated {
		m.logger.Warn().
			Str("session_id", sessionID).
			Str("current_state", session.State).
			Msg("Reject rejected: state conflict")
		return ErrInvalidState
	}

	// Notify caller
	rejectedMsg := protocol.MustNewMessage(protocol.TypeCallRejected, protocol.CallRejectedPayload{
		SessionID:  sessionID,
		CalleeInfo: calleeInfo,
	})

	if m.sender != nil {
		if err := m.sender.SendToUser(ctx, session.CallerID, rejectedMsg); err != nil {
			m.logger.Error().
				Str("session_id", sessionID).
				Str("caller_id", session.CallerID).
				Err(err).
				Msg("Failed to notify caller of rejection")
		}
	}

	// Clean up session
	if err := m.sessions.DeleteCallSession(ctx, sessionID); err != nil {
		m.logger.Error().
			Str("session_id", sessionID).
			Err(err).
			Msg("Failed to delete session after rejection")
	}

	m.logger.Info().
		Str("session_id", sessionID).
		Str("caller_id", session.CallerID).
		Str("callee_id", session.CalleeID).
		Msg("Call rejected")

	return nil
}

// EndCall ends an active call.
// Either party can end the call at any time.
func (m *CallManager) EndCall(ctx context.Context, userID, sessionID string, userInfo *protocol.UserInfo) error {
	m.logger.Info().
		Str("user_id", userID).
		Str("session_id", sessionID).
		Msg("Ending call")

	session, err := m.sessions.GetCallSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, redis.ErrSessionNotFound) {
			return ErrSessionNotFound
		}
		return err
	}

	// Either caller or callee can end the call
	if session.CallerID != userID && session.CalleeID != userID {
		m.logger.Warn().
			Str("user_id", userID).
			Str("session_id", sessionID).
			Msg("End rejected: not a participant")
		return ErrNotAuthorized
	}

	// Determine the peer to notify
	peerID := session.CallerID
	if userID == session.CallerID {
		peerID = session.CalleeID
	}

	// Mark as ended (we don't care about current state for ending)
	_, err = m.sessions.UpdateCallState(ctx, sessionID, session.State, protocol.StateEnded)
	if err != nil {
		// Log but continue with cleanup
		m.logger.Warn().
			Str("session_id", sessionID).
			Err(err).
			Msg("Failed to update state to ended")
	}

	// Notify peer
	endedMsg := protocol.MustNewMessage(protocol.TypeCallEnded, protocol.CallEndedPayload{
		SessionID: sessionID,
		Reason:    "ended_by_user",
		PeerInfo:  userInfo,
	})

	if m.sender != nil {
		if err := m.sender.SendToUser(ctx, peerID, endedMsg); err != nil {
			m.logger.Error().
				Str("session_id", sessionID).
				Str("peer_id", peerID).
				Err(err).
				Msg("Failed to notify peer of call end")
		}
	}

	// Clean up session
	if err := m.sessions.DeleteCallSession(ctx, sessionID); err != nil {
		m.logger.Error().
			Str("session_id", sessionID).
			Err(err).
			Msg("Failed to delete session after end")
	}

	m.logger.Info().
		Str("session_id", sessionID).
		Str("ended_by", userID).
		Str("peer_id", peerID).
		Msg("Call ended")

	return nil
}

// ForwardWebRTC forwards a WebRTC signaling message to the peer.
// Validates the user is part of the session and the call is in accepted state.
func (m *CallManager) ForwardWebRTC(ctx context.Context, userID, sessionID string, msg *protocol.Message) error {
	session, err := m.sessions.GetCallSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, redis.ErrSessionNotFound) {
			return ErrSessionNotFound
		}
		return err
	}

	// Verify user is part of the session
	if session.CallerID != userID && session.CalleeID != userID {
		return ErrNotAuthorized
	}

	// Verify call is in accepted state
	if session.State != protocol.StateAccepted {
		return ErrInvalidState
	}

	// Determine peer
	peerID := session.CallerID
	if userID == session.CallerID {
		peerID = session.CalleeID
	}

	// Forward message to peer
	if m.sender != nil {
		if err := m.sender.SendToUser(ctx, peerID, msg); err != nil {
			m.logger.Error().
				Str("session_id", sessionID).
				Str("from", userID).
				Str("to", peerID).
				Str("type", msg.Type).
				Err(err).
				Msg("Failed to forward WebRTC message")
			return err
		}
	}

	m.logger.Debug().
		Str("session_id", sessionID).
		Str("from", userID).
		Str("to", peerID).
		Str("type", msg.Type).
		Msg("WebRTC message forwarded")

	return nil
}

// HandleDisconnect handles cleanup when a user disconnects.
// Ends any active call and notifies the peer.
func (m *CallManager) HandleDisconnect(ctx context.Context, userID string) error {
	m.logger.Info().
		Str("user_id", userID).
		Msg("Handling user disconnect")

	session, err := m.sessions.CleanupUserCall(ctx, userID)
	if err != nil {
		return err
	}

	if session == nil {
		m.logger.Debug().
			Str("user_id", userID).
			Msg("No active call to cleanup")
		return nil
	}

	// Determine peer
	peerID := session.CallerID
	if userID == session.CallerID {
		peerID = session.CalleeID
	}

	// Notify peer
	endedMsg := protocol.MustNewMessage(protocol.TypeCallEnded, protocol.CallEndedPayload{
		SessionID: session.SessionID,
		Reason:    "peer_disconnected",
	})

	if m.sender != nil {
		if err := m.sender.SendToUser(ctx, peerID, endedMsg); err != nil {
			m.logger.Error().
				Str("session_id", session.SessionID).
				Str("peer_id", peerID).
				Err(err).
				Msg("Failed to notify peer of disconnect")
		}
	}

	m.logger.Info().
		Str("session_id", session.SessionID).
		Str("disconnected_user", userID).
		Str("peer_id", peerID).
		Msg("Call ended due to disconnect")

	return nil
}
