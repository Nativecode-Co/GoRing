# Goring - WebSocket Signaling Server for Voice Calling

A production-ready Golang WebSocket signaling server for voice calling with Redis-backed state management and WebRTC support.

## Features

- **Single WebSocket per user** - Enforced via Redis with atomic SET NX
- **Redis as single source of truth** - No in-memory session state
- **JWT authentication** - Validated during WebSocket upgrade
- **Cross-instance messaging** - Redis pub/sub for horizontal scaling
- **Race-condition safe** - Lua scripts for atomic state transitions
- **Graceful shutdown** - Clean connection handling on SIGINT/SIGTERM
- **Full WebRTC signaling** - SDP offer/answer and ICE candidate exchange
- **Working example client** - React + TypeScript web client included

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Client A  │     │   Client B  │     │   Client C  │
└──────┬──────┘     └──────┬──────┘     └──────┬──────┘
       │                   │                   │
       │ WebSocket         │ WebSocket         │ WebSocket
       │                   │                   │
       ▼                   ▼                   ▼
┌──────────────────────────────────────────────────────┐
│                    Server Instance 1                  │
│  ┌─────────┐  ┌────────────┐  ┌──────────────────┐  │
│  │   Hub   │──│CallManager │──│ SessionManager   │  │
│  └────┬────┘  └────────────┘  └────────┬─────────┘  │
│       │                                │             │
│       └────────────┬───────────────────┘             │
│                    │                                 │
└────────────────────┼─────────────────────────────────┘
                     │
                     ▼ Redis
┌──────────────────────────────────────────────────────┐
│  ┌─────────────────┐  ┌──────────────────────────┐  │
│  │  Sessions/Keys   │  │   Pub/Sub Channels       │  │
│  │  ws:user:*       │  │   ws:signal:*            │  │
│  │  call:session:*  │  │                          │  │
│  │  call:user:*     │  │                          │  │
│  └─────────────────┘  └──────────────────────────┘  │
└──────────────────────────────────────────────────────┘
```

## Project Structure

```
goring/
├── cmd/server/            # Application entry point
├── internal/
│   ├── auth/             # JWT token validation
│   ├── protocol/         # WebSocket message definitions
│   ├── redis/            # Redis client and session management
│   ├── signaling/        # Call logic and pub/sub
│   └── ws/               # WebSocket hub and client handlers
├── examples/
│   └── web-client/       # React + TypeScript example client
│       ├── src/
│       │   ├── lib/SignalingClient.ts   # WebRTC/WebSocket client
│       │   ├── components/CallUI.tsx     # Call interface component
│       │   └── App.tsx                   # Main application
│       └── package.json
├── docker-compose.yml    # Docker orchestration
├── Dockerfile           # Multi-stage build
└── .env.example         # Configuration template
```

## Quick Start

### Option 1: Try the Example Client (Recommended for Testing)

The fastest way to see Goring in action is to use the included React example client:

```bash
# Start server and Redis with Docker
docker compose up -d

# In a separate terminal, run the example web client
cd examples/web-client
npm install
npm run dev
```

Open the URL shown (typically `http://localhost:5173`) in **two different browser tabs**. You can now make test calls between the tabs using the demo JWT tokens provided in the UI.

### Option 2: Docker Only

```bash
# Start server with Redis
docker compose up -d

# Test horizontal scaling (two server instances)
docker compose --profile scaling up -d

# View logs
docker compose logs -f server

# Stop
docker compose down
```

The server will be available at `ws://localhost:8080/ws?token=<JWT>`

### Option 3: Local Development

**Prerequisites:** Go 1.21+, Redis 6+

```bash
# Set environment variables
export JWT_SECRET="your-secret-key"
export REDIS_ADDR="localhost:6379"
export PORT="8080"

# Run the server
go run ./cmd/server
```

## Testing with JWT Tokens

For development and testing, you can generate test JWT tokens:

```go
// The auth package includes a test token generator
// In production, use your auth service to generate tokens

// Example JWT payload structure:
{
  "hash": "user-123",        // User ID (required)
  "name": "Alice",
  "username": "alice",
  "iat": 1768720767,
  "exp": 1768732667          // Token expiration
}
```

Connect via WebSocket using wscat or any WebSocket client:

```bash
wscat -c "ws://localhost:8080/ws?token=<JWT>"
```

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP server port |
| `JWT_SECRET` | `dev-secret-change-in-production` | Secret key for JWT validation |
| `REDIS_ADDR` | `localhost:6379` | Redis server address |
| `REDIS_PASSWORD` | (empty) | Redis password |
| `SERVER_ID` | (hostname) | Unique server instance identifier |

### Environment Files

```bash
# Copy the example file
cp .env.example .env

# Edit with your values
nano .env
```

The `.env` file is gitignored. Docker Compose automatically loads it.

## WebSocket Protocol

All messages follow this envelope format:

```json
{
  "type": "event.name",
  "payload": {}
}
```

### Call Flow with WebRTC Signaling

```
Caller                    Server                    Callee
   │                         │                         │
   │──call.start────────────▶│                         │
   │  {callee_id: "userB"}   │                         │
   │                         │──call.ring─────────────▶│
   │                         │  {session_id, caller_id}│
   │                         │                         │
   │                         │◀────────call.accept─────│
   │                         │  {session_id}           │
   │◀───call.accepted────────│                         │
   │  {session_id}           │                         │
   │                         │                         │
   │  ─────────── WebRTC Signaling Phase ───────────  │
   │                         │                         │
   │──webrtc.offer──────────▶│──webrtc.offer─────────▶│
   │  {session_id, sdp}      │  {session_id, sdp}     │
   │                         │                         │
   │◀─────────webrtc.answer──│◀─────────webrtc.answer─│
   │  {session_id, sdp}      │  {session_id, sdp}     │
   │                         │                         │
   │──webrtc.ice────────────▶│──webrtc.ice───────────▶│
   │◀─────────────webrtc.ice─│◀────────────webrtc.ice─│
   │  (ICE candidates exchanged in both directions)   │
   │                         │                         │
   │  ═══════════ P2P Media Connection ═══════════   │
   │                         │                         │
   │──call.end──────────────▶│                         │
   │  {session_id}           │                         │
   │                         │──call.ended────────────▶│
   │                         │  {session_id, reason}   │
```

### Message Types

#### Client → Server

**call.start** - Initiate a call
```json
{
  "type": "call.start",
  "payload": {
    "callee_id": "user-456"
  }
}
```

**call.accept** - Accept incoming call
```json
{
  "type": "call.accept",
  "payload": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000"
  }
}
```

**call.reject** - Reject incoming call
```json
{
  "type": "call.reject",
  "payload": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000"
  }
}
```

**call.end** - End active call
```json
{
  "type": "call.end",
  "payload": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000"
  }
}
```

#### WebRTC Signaling (Bidirectional)

These messages are forwarded to the peer. Only valid after call is accepted.

**webrtc.offer** - Send SDP offer (caller → callee)
```json
{
  "type": "webrtc.offer",
  "payload": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "sdp": "v=0\r\no=- 123456 2 IN IP4 127.0.0.1\r\n..."
  }
}
```

**webrtc.answer** - Send SDP answer (callee → caller)
```json
{
  "type": "webrtc.answer",
  "payload": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "sdp": "v=0\r\no=- 789012 2 IN IP4 127.0.0.1\r\n..."
  }
}
```

**webrtc.ice** - Send ICE candidate (both directions)
```json
{
  "type": "webrtc.ice",
  "payload": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "candidate": "candidate:1 1 UDP 2122252543 192.168.1.1 12345 typ host"
  }
}
```

#### Server → Client

**call.ring** - Incoming call notification
```json
{
  "type": "call.ring",
  "payload": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "caller_id": "user-123"
  }
}
```

**call.accepted** - Call was accepted
```json
{
  "type": "call.accepted",
  "payload": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000"
  }
}
```

**call.rejected** - Call was rejected
```json
{
  "type": "call.rejected",
  "payload": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000"
  }
}
```

**call.ended** - Call has ended
```json
{
  "type": "call.ended",
  "payload": {
    "session_id": "550e8400-e29b-41d4-a716-446655440000",
    "reason": "ended_by_user"
  }
}
```

**error** - Error occurred
```json
{
  "type": "error",
  "payload": {
    "code": "user_busy",
    "message": "User is busy"
  }
}
```

### Error Codes

| Code | Description |
|------|-------------|
| `invalid_message` | Malformed message or unknown type |
| `unauthorized` | Not authorized for this operation |
| `user_busy` | Target user is already in a call |
| `user_offline` | Target user is not connected |
| `session_not_found` | Call session does not exist |
| `invalid_state` | Invalid state transition (e.g., double accept) |
| `internal_error` | Server-side error |

## Redis Data Model

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `ws:user:{user_id}` | STRING | 30s | User's WebSocket server ID |
| `call:session:{session_id}` | HASH | 120s | Call session state |
| `call:user:{user_id}` | STRING | 120s | User's active call session |

### call:session Hash Fields

```
caller_id   - ID of the user who initiated the call
callee_id   - ID of the user being called
state       - Current state: ringing | accepted | rejected | ended
created_at  - Unix timestamp of session creation
```

## Horizontal Scaling

The server supports horizontal scaling via Redis pub/sub:

1. Each instance subscribes to channels for its connected users
2. When sending a message to a user on another instance, publish to their channel
3. The instance with the connection receives and forwards the message

```
┌──────────────┐          ┌──────────────┐
│  Instance 1  │          │  Instance 2  │
│  (User A)    │          │  (User B)    │
└──────┬───────┘          └──────┬───────┘
       │                         │
       │    Redis Pub/Sub        │
       │  ws:signal:userB        │
       └─────────────────────────┘
```

## Health Check

```bash
curl http://localhost:8080/health
```

Returns `200 OK` if the server and Redis are healthy.

## Example Client

The project includes a fully functional React + TypeScript web client that demonstrates complete WebRTC integration.

### Features of Example Client

- Complete call lifecycle (initiate, ring, accept/reject, end)
- Real-time WebRTC audio calling
- Call duration timer
- Connection status indicators
- Error handling with user-friendly messages
- Microphone permission handling

### Running the Example

```bash
# Start the server
docker compose up -d

# Install and run the example client
cd examples/web-client
npm install
npm run dev
```

Open two browser tabs with the provided URL to test calls between different users.

### Example Client Architecture

The example client consists of:

- **[SignalingClient.ts](examples/web-client/src/lib/SignalingClient.ts)** - Core WebSocket and WebRTC integration
  - Manages WebSocket connection to signaling server
  - Handles WebRTC peer connection setup
  - State machine for call lifecycle
  - SDP offer/answer negotiation
  - ICE candidate exchange

- **[CallUI.tsx](examples/web-client/src/components/CallUI.tsx)** - React UI component
  - Dynamic UI based on call state
  - Incoming call notifications
  - Active call timer
  - Call controls

- **[App.tsx](examples/web-client/src/App.tsx)** - Main application
  - Connection management
  - JWT token input
  - Server URL configuration

## Development

```bash
# Build the Go server
go build -o server ./cmd/server

# Run with verbose logging
./server

# Run tests
go test ./...

# Build example client for production
cd examples/web-client
npm run build
```

## Client Integration Guide

### JWT Token Format

The server expects JWT tokens with a `hash` claim as the user ID:

```json
{
  "hash": "6217138344386500",
  "name": "User Name",
  "username": "username",
  "user_type": "16587740479514765",
  "is_admin": "0",
  "iat": 1768720767,
  "exp": 1768732667
}
```

**Note:** A complete, working example client is available in [examples/web-client](examples/web-client). The code snippets below show the core integration pattern.

### JavaScript/TypeScript Example

```typescript
class SignalingClient {
  private ws: WebSocket;
  private sessionId: string | null = null;
  private pc: RTCPeerConnection | null = null;

  constructor(private serverUrl: string, private token: string) {}

  connect() {
    this.ws = new WebSocket(`${this.serverUrl}/ws?token=${this.token}`);
    this.ws.onmessage = (event) => this.handleMessage(JSON.parse(event.data));
  }

  private handleMessage(msg: { type: string; payload: any }) {
    switch (msg.type) {
      case 'call.ring':
        // Incoming call - show UI to accept/reject
        this.sessionId = msg.payload.session_id;
        this.onIncomingCall?.(msg.payload.caller_id, msg.payload.session_id);
        break;

      case 'call.accepted':
        // Call accepted - start WebRTC as caller
        this.sessionId = msg.payload.session_id;
        this.startWebRTC(true);
        break;

      case 'webrtc.offer':
        // Received offer - create answer
        this.handleOffer(msg.payload.sdp);
        break;

      case 'webrtc.answer':
        // Received answer - set remote description
        this.pc?.setRemoteDescription({ type: 'answer', sdp: msg.payload.sdp });
        break;

      case 'webrtc.ice':
        // Received ICE candidate
        if (msg.payload.candidate) {
          this.pc?.addIceCandidate({ candidate: msg.payload.candidate });
        }
        break;

      case 'call.ended':
        this.cleanup();
        this.onCallEnded?.(msg.payload.reason);
        break;

      case 'error':
        this.onError?.(msg.payload.code, msg.payload.message);
        break;
    }
  }

  // Initiate a call
  startCall(calleeId: string) {
    this.send('call.start', { callee_id: calleeId });
  }

  // Accept incoming call
  acceptCall(sessionId: string) {
    this.sessionId = sessionId;
    this.send('call.accept', { session_id: sessionId });
    this.startWebRTC(false); // Start as callee
  }

  // Reject incoming call
  rejectCall(sessionId: string) {
    this.send('call.reject', { session_id: sessionId });
  }

  // End active call
  endCall() {
    if (this.sessionId) {
      this.send('call.end', { session_id: this.sessionId });
      this.cleanup();
    }
  }

  private async startWebRTC(isCaller: boolean) {
    this.pc = new RTCPeerConnection({
      iceServers: [{ urls: 'stun:stun.l.google.com:19302' }]
    });

    // Handle ICE candidates
    this.pc.onicecandidate = (event) => {
      if (event.candidate) {
        this.send('webrtc.ice', {
          session_id: this.sessionId,
          candidate: event.candidate.candidate
        });
      }
    };

    // Handle incoming tracks
    this.pc.ontrack = (event) => {
      this.onRemoteStream?.(event.streams[0]);
    };

    // Add local audio track
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    stream.getTracks().forEach(track => this.pc!.addTrack(track, stream));

    if (isCaller) {
      // Create and send offer
      const offer = await this.pc.createOffer();
      await this.pc.setLocalDescription(offer);
      this.send('webrtc.offer', {
        session_id: this.sessionId,
        sdp: offer.sdp
      });
    }
  }

  private async handleOffer(sdp: string) {
    if (!this.pc) return;
    await this.pc.setRemoteDescription({ type: 'offer', sdp });
    const answer = await this.pc.createAnswer();
    await this.pc.setLocalDescription(answer);
    this.send('webrtc.answer', {
      session_id: this.sessionId,
      sdp: answer.sdp
    });
  }

  private send(type: string, payload: any) {
    this.ws.send(JSON.stringify({ type, payload }));
  }

  private cleanup() {
    this.pc?.close();
    this.pc = null;
    this.sessionId = null;
  }

  // Event callbacks
  onIncomingCall?: (callerId: string, sessionId: string) => void;
  onCallEnded?: (reason: string) => void;
  onRemoteStream?: (stream: MediaStream) => void;
  onError?: (code: string, message: string) => void;
}

// Usage
const client = new SignalingClient('ws://localhost:8080', 'your-jwt-token');
client.onIncomingCall = (callerId, sessionId) => {
  if (confirm(`Incoming call from ${callerId}`)) {
    client.acceptCall(sessionId);
  } else {
    client.rejectCall(sessionId);
  }
};
client.onRemoteStream = (stream) => {
  document.querySelector('audio')!.srcObject = stream;
};
client.connect();
```

### Flutter/Dart Example

```dart
import 'dart:convert';
import 'package:web_socket_channel/web_socket_channel.dart';
import 'package:flutter_webrtc/flutter_webrtc.dart';

class SignalingClient {
  late WebSocketChannel _channel;
  RTCPeerConnection? _pc;
  String? _sessionId;
  final String serverUrl;
  final String token;

  Function(String callerId, String sessionId)? onIncomingCall;
  Function(MediaStream stream)? onRemoteStream;
  Function(String reason)? onCallEnded;

  SignalingClient({required this.serverUrl, required this.token});

  void connect() {
    _channel = WebSocketChannel.connect(Uri.parse('$serverUrl/ws?token=$token'));
    _channel.stream.listen(_handleMessage);
  }

  void _handleMessage(dynamic data) {
    final msg = jsonDecode(data);
    switch (msg['type']) {
      case 'call.ring':
        _sessionId = msg['payload']['session_id'];
        onIncomingCall?.call(msg['payload']['caller_id'], _sessionId!);
        break;
      case 'call.accepted':
        _sessionId = msg['payload']['session_id'];
        _startWebRTC(isCaller: true);
        break;
      case 'webrtc.offer':
        _handleOffer(msg['payload']['sdp']);
        break;
      case 'webrtc.answer':
        _pc?.setRemoteDescription(RTCSessionDescription(msg['payload']['sdp'], 'answer'));
        break;
      case 'webrtc.ice':
        if (msg['payload']['candidate'] != null) {
          _pc?.addCandidate(RTCIceCandidate(msg['payload']['candidate'], '', 0));
        }
        break;
      case 'call.ended':
        _cleanup();
        onCallEnded?.call(msg['payload']['reason']);
        break;
    }
  }

  void startCall(String calleeId) {
    _send('call.start', {'callee_id': calleeId});
  }

  void acceptCall(String sessionId) {
    _sessionId = sessionId;
    _send('call.accept', {'session_id': sessionId});
    _startWebRTC(isCaller: false);
  }

  void rejectCall(String sessionId) {
    _send('call.reject', {'session_id': sessionId});
  }

  void endCall() {
    if (_sessionId != null) {
      _send('call.end', {'session_id': _sessionId});
      _cleanup();
    }
  }

  Future<void> _startWebRTC({required bool isCaller}) async {
    _pc = await createPeerConnection({
      'iceServers': [{'urls': 'stun:stun.l.google.com:19302'}]
    });

    _pc!.onIceCandidate = (candidate) {
      _send('webrtc.ice', {
        'session_id': _sessionId,
        'candidate': candidate.candidate
      });
    };

    _pc!.onTrack = (event) {
      if (event.streams.isNotEmpty) {
        onRemoteStream?.call(event.streams[0]);
      }
    };

    final stream = await navigator.mediaDevices.getUserMedia({'audio': true});
    stream.getTracks().forEach((track) => _pc!.addTrack(track, stream));

    if (isCaller) {
      final offer = await _pc!.createOffer();
      await _pc!.setLocalDescription(offer);
      _send('webrtc.offer', {'session_id': _sessionId, 'sdp': offer.sdp});
    }
  }

  Future<void> _handleOffer(String sdp) async {
    await _pc?.setRemoteDescription(RTCSessionDescription(sdp, 'offer'));
    final answer = await _pc?.createAnswer();
    await _pc?.setLocalDescription(answer!);
    _send('webrtc.answer', {'session_id': _sessionId, 'sdp': answer!.sdp});
  }

  void _send(String type, Map<String, dynamic> payload) {
    _channel.sink.add(jsonEncode({'type': type, 'payload': payload}));
  }

  void _cleanup() {
    _pc?.close();
    _pc = null;
    _sessionId = null;
  }
}
```

## Backend Architecture

### Components

- **[cmd/server/main.go](cmd/server/main.go)** - Application entry point
  - Initializes Redis, Hub, CallManager, and PubSub
  - Sets up HTTP server with `/ws` and `/health` endpoints
  - Handles graceful shutdown

- **[internal/auth/jwt.go](internal/auth/jwt.go)** - JWT authentication
  - Validates tokens using HMAC-SHA256
  - Extracts user ID from `hash` claim
  - Test token generation for development

- **[internal/protocol/messages.go](internal/protocol/messages.go)** - Protocol definitions
  - All WebSocket message types and structures
  - Call states and error codes

- **[internal/redis/](internal/redis/)** - Redis integration
  - **client.go** - Redis client wrapper
  - **session.go** - Session and state management with atomic operations

- **[internal/signaling/](internal/signaling/)** - Call orchestration
  - **call.go** - Call lifecycle management (start, accept, reject, end)
  - **pubsub.go** - Cross-instance messaging for horizontal scaling

- **[internal/ws/](internal/ws/)** - WebSocket handling
  - **handler.go** - Hub for connection management and message routing
  - **client.go** - Individual WebSocket connection handling

### Key Design Patterns

- **Stateless servers** - All state stored in Redis for horizontal scalability
- **Atomic operations** - Lua scripts prevent race conditions
- **Pub/Sub architecture** - Cross-instance messaging via Redis channels
- **Connection deduplication** - Redis SET NX ensures single connection per user
- **Automatic cleanup** - TTLs and disconnect handlers prevent orphaned sessions
- **Heartbeat mechanism** - Periodic Redis TTL refresh keeps sessions alive

## Technologies

### Backend
- **Go 1.21+** with standard library HTTP server
- **gorilla/websocket** for WebSocket connections
- **redis/go-redis/v9** for state management
- **golang-jwt/jwt/v5** for authentication
- **rs/zerolog** for structured logging

### Frontend (Example Client)
- **React 18** with TypeScript 5
- **Vite 5** for development and building
- **Native WebRTC API** for peer-to-peer media
- **Native WebSocket API** for signaling

### Infrastructure
- **Redis 7** for state and pub/sub
- **Docker** and **Docker Compose** for deployment

## Production Considerations

### Security
- Always use secure JWT secrets in production
- Consider rate limiting on WebSocket connections
- Use WSS (WebSocket Secure) in production
- Implement user authentication in your auth service
- Validate and sanitize all client inputs

### Scaling
- The server is stateless and can scale horizontally
- Use a load balancer (e.g., nginx, HAProxy) in front of multiple instances
- Redis can be clustered for high availability
- Monitor Redis memory usage and set appropriate TTLs

### Monitoring
- Health check endpoint at `/health` for load balancer checks
- Structured JSON logs for aggregation (ELK, Datadog, etc.)
- Monitor WebSocket connection count
- Track call success/failure rates
- Monitor Redis latency and memory

## License

MIT
