package protocol

import (
	"encoding/json"
	"fmt"
)

// Message types for WebSocket communication
const (
	// Client -> Server (Call Control)
	TypeCallStart  = "call.start"
	TypeCallAccept = "call.accept"
	TypeCallReject = "call.reject"
	TypeCallEnd    = "call.end"

	// Client <-> Server (WebRTC Signaling - relayed to peer)
	TypeWebRTCOffer  = "webrtc.offer"
	TypeWebRTCAnswer = "webrtc.answer"
	TypeWebRTCICE    = "webrtc.ice"

	// Server -> Client
	TypeCallRing     = "call.ring"
	TypeCallAccepted = "call.accepted"
	TypeCallRejected = "call.rejected"
	TypeCallEnded    = "call.ended"
	TypeError        = "error"
)

// Call states stored in Redis
const (
	StateRinging  = "ringing"
	StateAccepted = "accepted"
	StateRejected = "rejected"
	StateEnded    = "ended"
)

// Message is the envelope for all WebSocket messages
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// UserInfo contains user profile data for event payloads
type UserInfo struct {
	UserID       string `json:"user_id"`
	Name         string `json:"name,omitempty"`
	Username     string `json:"username,omitempty"`
	ImageProfile string `json:"image_profile,omitempty"`
}

// CallStartPayload is sent by caller to initiate a call
type CallStartPayload struct {
	CalleeID string `json:"callee_id"`
}

// CallRingPayload is sent to callee when incoming call
type CallRingPayload struct {
	SessionID  string    `json:"session_id"`
	CallerID   string    `json:"caller_id"`
	CallerInfo *UserInfo `json:"caller_info,omitempty"`
}

// CallSessionPayload is used for accept/reject/end operations
type CallSessionPayload struct {
	SessionID string `json:"session_id"`
}

// CallAcceptedPayload is sent to caller when call is accepted
type CallAcceptedPayload struct {
	SessionID  string    `json:"session_id"`
	CalleeInfo *UserInfo `json:"callee_info,omitempty"`
}

// CallRejectedPayload is sent to caller when call is rejected
type CallRejectedPayload struct {
	SessionID  string    `json:"session_id"`
	CalleeInfo *UserInfo `json:"callee_info,omitempty"`
}

// CallEndedPayload is sent to both parties when call ends
type CallEndedPayload struct {
	SessionID string    `json:"session_id"`
	Reason    string    `json:"reason"`
	PeerInfo  *UserInfo `json:"peer_info,omitempty"`
}

// WebRTCOfferPayload is sent by caller to initiate WebRTC connection
type WebRTCOfferPayload struct {
	SessionID string `json:"session_id"`
	SDP       string `json:"sdp"`
}

// WebRTCAnswerPayload is sent by callee in response to offer
type WebRTCAnswerPayload struct {
	SessionID string `json:"session_id"`
	SDP       string `json:"sdp"`
}

// WebRTCICEPayload is sent by both parties for ICE candidate exchange
type WebRTCICEPayload struct {
	SessionID     string `json:"session_id"`
	Candidate     string `json:"candidate"`
	SDPMid        string `json:"sdpMid,omitempty"`
	SDPMLineIndex *int   `json:"sdpMLineIndex,omitempty"`
}

// ErrorPayload is sent when an error occurs
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error codes
const (
	ErrCodeInvalidMessage  = "invalid_message"
	ErrCodeUnauthorized    = "unauthorized"
	ErrCodeUserBusy        = "user_busy"
	ErrCodeUserOffline     = "user_offline"
	ErrCodeSessionNotFound = "session_not_found"
	ErrCodeInvalidState    = "invalid_state"
	ErrCodeInternalError   = "internal_error"
)

// NewMessage creates a new message with the given type and payload
func NewMessage(msgType string, payload any) (*Message, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return &Message{
		Type:    msgType,
		Payload: data,
	}, nil
}

// MustNewMessage creates a new message, panics on error
func MustNewMessage(msgType string, payload any) *Message {
	msg, err := NewMessage(msgType, payload)
	if err != nil {
		panic(err)
	}
	return msg
}

// ParsePayload unmarshals the payload into the given target
func (m *Message) ParsePayload(target any) error {
	if err := json.Unmarshal(m.Payload, target); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}
	return nil
}

// Bytes serializes the message to JSON bytes
func (m *Message) Bytes() ([]byte, error) {
	return json.Marshal(m)
}

// MustBytes serializes the message, panics on error
func (m *Message) MustBytes() []byte {
	data, err := m.Bytes()
	if err != nil {
		panic(err)
	}
	return data
}

// ParseMessage parses a JSON message from bytes
func ParseMessage(data []byte) (*Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal message: %w", err)
	}
	return &msg, nil
}

// NewErrorMessage creates an error message
func NewErrorMessage(code, message string) *Message {
	return MustNewMessage(TypeError, ErrorPayload{
		Code:    code,
		Message: message,
	})
}
