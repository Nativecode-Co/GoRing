# Goring Web Client Example

A simple React + TypeScript web application demonstrating voice calling with the Goring signaling server.

## Prerequisites

- Node.js 18+
- npm or yarn
- Running Goring server with Redis

## Quick Start

1. **Start the server** (from project root):
   ```bash
   docker compose up -d
   ```

2. **Install dependencies**:
   ```bash
   cd examples/web-client
   npm install
   ```

3. **Start the dev server**:
   ```bash
   npm run dev
   ```

4. **Open in browser**: http://localhost:5173

## Testing Calls

To test voice calling, you need two users:

1. Open two browser tabs (or windows)
2. In each tab, enter a different JWT token
3. Connect both tabs to the server
4. From Tab A, enter Tab B's user ID (the `hash` claim from the JWT)
5. Click "Call" in Tab A
6. Accept the call in Tab B

## JWT Token Format

The server expects JWT tokens with this structure:

```json
{
  "hash": "user-123",
  "name": "User Name",
  "iat": 1234567890,
  "exp": 1234567890
}
```

The `hash` field is used as the user ID.

### Generate Test Tokens

For testing, you can use [jwt.io](https://jwt.io) to create tokens:

1. Go to jwt.io
2. Set the algorithm to HS256
3. Create a payload with `hash`, `iat`, and `exp` fields
4. Use the same secret as your server's `JWT_SECRET`

Example payload for User A:
```json
{
  "hash": "user-a-123",
  "name": "User A",
  "iat": 1700000000,
  "exp": 1800000000
}
```

Example payload for User B:
```json
{
  "hash": "user-b-456",
  "name": "User B",
  "iat": 1700000000,
  "exp": 1800000000
}
```

## Project Structure

```
src/
├── main.tsx              # Entry point
├── App.tsx               # Main app component
├── App.css               # App styles
├── lib/
│   └── SignalingClient.ts  # WebSocket & WebRTC client
└── components/
    ├── CallUI.tsx        # Call interface component
    └── CallUI.css        # CallUI styles
```

## Features

- Connect/disconnect from signaling server
- Initiate voice calls
- Receive and accept/reject incoming calls
- End active calls
- Real-time call status display
- Call duration timer
- Error handling

## Notes

- **Microphone Access**: The browser will request microphone permission when a call is established
- **HTTPS**: For production, WebRTC requires HTTPS (localhost is an exception)
- **STUN/TURN**: This example uses Google's public STUN servers. For production, consider using your own TURN servers
