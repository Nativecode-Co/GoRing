import { useState, useRef, useEffect } from 'react';
import { SignalingClient, CallState } from '../lib/SignalingClient';
import './CallUI.css';

interface CallUIProps {
  client: SignalingClient;
}

export function CallUI({ client }: CallUIProps) {
  const [state, setState] = useState<CallState>('idle');
  const [calleeId, setCalleeId] = useState('');
  const [incomingCall, setIncomingCall] = useState<{ callerId: string; sessionId: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const audioRef = useRef<HTMLAudioElement>(null);

  useEffect(() => {
    client.setEvents({
      onStateChange: (newState) => {
        setState(newState);
        if (newState === 'idle') {
          setIncomingCall(null);
        }
      },
      onIncomingCall: (callerId, sessionId) => {
        setIncomingCall({ callerId, sessionId });
      },
      onCallEnded: (reason) => {
        setError(`Call ended: ${reason}`);
        setTimeout(() => setError(null), 3000);
      },
      onRemoteStream: (stream) => {
        if (audioRef.current) {
          audioRef.current.srcObject = stream;
          audioRef.current.play().catch(console.error);
        }
      },
      onError: (code, message) => {
        setError(`${code}: ${message}`);
        setTimeout(() => setError(null), 5000);
      }
    });

    return () => {
      client.setEvents({});
    };
  }, [client]);

  const handleStartCall = () => {
    if (calleeId.trim()) {
      setError(null);
      client.startCall(calleeId.trim());
    }
  };

  const handleAccept = () => {
    if (incomingCall) {
      client.acceptCall(incomingCall.sessionId);
      setIncomingCall(null);
    }
  };

  const handleReject = () => {
    if (incomingCall) {
      client.rejectCall(incomingCall.sessionId);
      setIncomingCall(null);
    }
  };

  const handleEndCall = () => {
    client.endCall();
  };

  return (
    <div className="call-ui">
      <audio ref={audioRef} autoPlay />

      {error && <div className="error-banner">{error}</div>}

      <div className="status-indicator">
        <span className={`status-dot ${state}`} />
        <span className="status-text">{state}</span>
      </div>

      {/* Incoming call notification */}
      {incomingCall && (
        <div className="incoming-call">
          <p>Incoming call from: <strong>{incomingCall.callerId}</strong></p>
          <div className="call-actions">
            <button className="btn accept" onClick={handleAccept}>
              Accept
            </button>
            <button className="btn reject" onClick={handleReject}>
              Reject
            </button>
          </div>
        </div>
      )}

      {/* Idle state - can make calls */}
      {state === 'idle' && !incomingCall && (
        <div className="call-form">
          <input
            type="text"
            placeholder="Enter user ID to call"
            value={calleeId}
            onChange={(e) => setCalleeId(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleStartCall()}
          />
          <button className="btn call" onClick={handleStartCall} disabled={!calleeId.trim()}>
            Call
          </button>
        </div>
      )}

      {/* Calling state */}
      {state === 'calling' && (
        <div className="calling-state">
          <p>Calling...</p>
          <button className="btn end" onClick={handleEndCall}>
            Cancel
          </button>
        </div>
      )}

      {/* Connected state */}
      {state === 'connected' && (
        <div className="connected-state">
          <p>Call in progress</p>
          <div className="call-timer">
            <CallTimer />
          </div>
          <button className="btn end" onClick={handleEndCall}>
            End Call
          </button>
        </div>
      )}
    </div>
  );
}

function CallTimer() {
  const [seconds, setSeconds] = useState(0);

  useEffect(() => {
    const interval = setInterval(() => {
      setSeconds((s) => s + 1);
    }, 1000);
    return () => clearInterval(interval);
  }, []);

  const mins = Math.floor(seconds / 60);
  const secs = seconds % 60;
  return (
    <span>
      {mins.toString().padStart(2, '0')}:{secs.toString().padStart(2, '0')}
    </span>
  );
}
