import { useState, useRef, useEffect } from 'react';
import { SignalingClient, CallState, UserInfo } from '../lib/SignalingClient';
import './CallUI.css';

interface CallUIProps {
  client: SignalingClient;
}

interface IncomingCall {
  callerId: string;
  sessionId: string;
  callerInfo?: UserInfo;
}

export function CallUI({ client }: CallUIProps) {
  const [state, setState] = useState<CallState>('idle');
  const [calleeId, setCalleeId] = useState('');
  const [incomingCall, setIncomingCall] = useState<IncomingCall | null>(null);
  const [peerInfo, setPeerInfo] = useState<UserInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const audioRef = useRef<HTMLAudioElement>(null);

  useEffect(() => {
    client.setEvents({
      onStateChange: (newState) => {
        setState(newState);
        if (newState === 'idle') {
          setIncomingCall(null);
          setPeerInfo(null);
        }
      },
      onIncomingCall: (callerId, sessionId, callerInfo) => {
        setIncomingCall({ callerId, sessionId, callerInfo });
      },
      onCallAccepted: (_sessionId, calleeInfo) => {
        if (calleeInfo) {
          setPeerInfo(calleeInfo);
        }
      },
      onCallEnded: (reason, endedByInfo) => {
        const peerName = endedByInfo?.name || endedByInfo?.username || '';
        const message = peerName ? `Call ended by ${peerName}: ${reason}` : `Call ended: ${reason}`;
        setError(message);
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
      // Set peer info from the incoming call's caller info
      if (incomingCall.callerInfo) {
        setPeerInfo(incomingCall.callerInfo);
      }
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
          {incomingCall.callerInfo?.image_profile && (
            <img
              src={incomingCall.callerInfo.image_profile}
              alt="Caller"
              className="caller-avatar"
            />
          )}
          <p>
            Incoming call from:{' '}
            <strong>
              {incomingCall.callerInfo?.name ||
               incomingCall.callerInfo?.username ||
               incomingCall.callerId}
            </strong>
            {incomingCall.callerInfo?.username && incomingCall.callerInfo?.name && (
              <span className="caller-username">@{incomingCall.callerInfo.username}</span>
            )}
          </p>
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
          {peerInfo?.image_profile && (
            <img
              src={peerInfo.image_profile}
              alt="Peer"
              className="peer-avatar"
            />
          )}
          <p>
            {peerInfo?.name || peerInfo?.username
              ? `Call with ${peerInfo.name || peerInfo.username}`
              : 'Call in progress'}
          </p>
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
