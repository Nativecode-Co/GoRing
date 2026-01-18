import { useState, useRef } from 'react';
import { SignalingClient } from './lib/SignalingClient';
import { CallUI } from './components/CallUI';

function App() {
  const [serverUrl, setServerUrl] = useState('ws://localhost:8080');
  const [token, setToken] = useState('');
  const [connected, setConnected] = useState(false);
  const [connectionError, setConnectionError] = useState<string | null>(null);
  const clientRef = useRef<SignalingClient | null>(null);

  const handleConnect = () => {
    if (!token.trim()) {
      setConnectionError('Please enter a JWT token');
      return;
    }

    setConnectionError(null);
    const client = new SignalingClient(serverUrl, token);

    client.setEvents({
      onConnected: () => {
        setConnected(true);
        setConnectionError(null);
      },
      onDisconnected: () => {
        setConnected(false);
        clientRef.current = null;
      },
      onError: (code, message) => {
        if (code === 'connection_error') {
          setConnectionError(message);
        }
      }
    });

    client.connect();
    clientRef.current = client;
  };

  const handleDisconnect = () => {
    clientRef.current?.disconnect();
    clientRef.current = null;
    setConnected(false);
  };

  return (
    <div className="app">
      <header className="app-header">
        <h1>Goring Voice Call Demo</h1>
        <p>WebSocket Signaling Server Example</p>
      </header>

      <main className="app-main">
        {!connected ? (
          <div className="connect-form">
            <h2>Connect to Server</h2>

            {connectionError && (
              <div className="error-message">{connectionError}</div>
            )}

            <div className="form-group">
              <label htmlFor="server">Server URL</label>
              <input
                id="server"
                type="text"
                value={serverUrl}
                onChange={(e) => setServerUrl(e.target.value)}
                placeholder="ws://localhost:8080"
              />
            </div>

            <div className="form-group">
              <label htmlFor="token">JWT Token</label>
              <textarea
                id="token"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder="Paste your JWT token here..."
                rows={4}
              />
            </div>

            <button className="btn connect" onClick={handleConnect}>
              Connect
            </button>

            <div className="help-text">
              <p>
                Generate a JWT token with your auth service. The token must contain
                a <code>hash</code> claim with the user ID.
              </p>
            </div>
          </div>
        ) : (
          <div className="connected-view">
            <div className="connection-status">
              <span className="status-badge">Connected</span>
              <button className="btn disconnect" onClick={handleDisconnect}>
                Disconnect
              </button>
            </div>

            {clientRef.current && <CallUI client={clientRef.current} />}
          </div>
        )}
      </main>

      <footer className="app-footer">
        <p>
          Open this page in two browser tabs with different user tokens to test
          calling.
        </p>
      </footer>
    </div>
  );
}

export default App;
