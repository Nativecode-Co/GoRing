export type CallState = 'idle' | 'calling' | 'ringing' | 'connected';

export interface SignalingEvents {
  onStateChange?: (state: CallState) => void;
  onIncomingCall?: (callerId: string, sessionId: string) => void;
  onCallEnded?: (reason: string) => void;
  onRemoteStream?: (stream: MediaStream) => void;
  onError?: (code: string, message: string) => void;
  onConnected?: () => void;
  onDisconnected?: () => void;
}

export class SignalingClient {
  private ws: WebSocket | null = null;
  private pc: RTCPeerConnection | null = null;
  private sessionId: string | null = null;
  private localStream: MediaStream | null = null;
  private state: CallState = 'idle';
  private events: SignalingEvents = {};

  constructor(
    private serverUrl: string,
    private token: string
  ) {}

  setEvents(events: SignalingEvents) {
    this.events = events;
  }

  private setState(state: CallState) {
    this.state = state;
    this.events.onStateChange?.(state);
  }

  getState(): CallState {
    return this.state;
  }

  getSessionId(): string | null {
    return this.sessionId;
  }

  connect() {
    const wsUrl = `${this.serverUrl}/ws?token=${this.token}`;
    this.ws = new WebSocket(wsUrl);

    this.ws.onopen = () => {
      this.events.onConnected?.();
    };

    this.ws.onclose = () => {
      this.events.onDisconnected?.();
      this.cleanup();
    };

    this.ws.onerror = () => {
      this.events.onError?.('connection_error', 'WebSocket connection failed');
    };

    this.ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        this.handleMessage(msg);
      } catch {
        console.error('Failed to parse message:', event.data);
      }
    };
  }

  disconnect() {
    if (this.sessionId) {
      this.endCall();
    }
    this.ws?.close();
    this.ws = null;
  }

  private handleMessage(msg: { type: string; payload: Record<string, unknown> }) {
    switch (msg.type) {
      case 'call.ring':
        this.sessionId = msg.payload.session_id as string;
        this.setState('ringing');
        this.events.onIncomingCall?.(
          msg.payload.caller_id as string,
          msg.payload.session_id as string
        );
        break;

      case 'call.accepted':
        this.sessionId = msg.payload.session_id as string;
        this.setState('connected');
        this.startWebRTC(true);
        break;

      case 'call.rejected':
        this.setState('idle');
        this.sessionId = null;
        this.events.onCallEnded?.('rejected');
        break;

      case 'webrtc.offer':
        this.handleOffer(msg.payload.sdp as string);
        break;

      case 'webrtc.answer':
        this.pc?.setRemoteDescription({
          type: 'answer',
          sdp: msg.payload.sdp as string
        });
        break;

      case 'webrtc.ice':
        if (msg.payload.candidate) {
          this.pc?.addIceCandidate(new RTCIceCandidate({
            candidate: msg.payload.candidate as string,
            sdpMid: msg.payload.sdpMid as string,
            sdpMLineIndex: msg.payload.sdpMLineIndex as number
          }));
        }
        break;

      case 'call.ended':
        this.cleanup();
        this.events.onCallEnded?.(msg.payload.reason as string);
        break;

      case 'error':
        this.events.onError?.(
          msg.payload.code as string,
          msg.payload.message as string
        );
        // Reset state on certain errors
        if (['user_offline', 'user_busy', 'session_not_found'].includes(msg.payload.code as string)) {
          this.setState('idle');
          this.sessionId = null;
        }
        break;
    }
  }

  startCall(calleeId: string) {
    this.send('call.start', { callee_id: calleeId });
    this.setState('calling');
  }

  acceptCall(sessionId: string) {
    this.sessionId = sessionId;
    this.send('call.accept', { session_id: sessionId });
    this.setState('connected');
    this.startWebRTC(false);
  }

  rejectCall(sessionId: string) {
    this.send('call.reject', { session_id: sessionId });
    this.setState('idle');
    this.sessionId = null;
  }

  endCall() {
    if (this.sessionId) {
      this.send('call.end', { session_id: this.sessionId });
      this.cleanup();
    }
  }

  private async startWebRTC(isCaller: boolean) {
    this.pc = new RTCPeerConnection({
      iceServers: [
        { urls: 'stun:stun.l.google.com:19302' },
        { urls: 'stun:stun1.l.google.com:19302' }
      ]
    });

    this.pc.onicecandidate = (event) => {
      if (event.candidate) {
        this.send('webrtc.ice', {
          session_id: this.sessionId,
          candidate: event.candidate.candidate,
          sdpMid: event.candidate.sdpMid,
          sdpMLineIndex: event.candidate.sdpMLineIndex
        });
      }
    };

    this.pc.ontrack = (event) => {
      if (event.streams[0]) {
        this.events.onRemoteStream?.(event.streams[0]);
      }
    };

    this.pc.oniceconnectionstatechange = () => {
      if (this.pc?.iceConnectionState === 'disconnected' ||
          this.pc?.iceConnectionState === 'failed') {
        this.events.onError?.('ice_failed', 'ICE connection failed');
      }
    };

    try {
      this.localStream = await navigator.mediaDevices.getUserMedia({ audio: true });
      this.localStream.getTracks().forEach(track => {
        this.pc!.addTrack(track, this.localStream!);
      });

      if (isCaller) {
        const offer = await this.pc.createOffer();
        await this.pc.setLocalDescription(offer);
        this.send('webrtc.offer', {
          session_id: this.sessionId,
          sdp: offer.sdp
        });
      }
    } catch (err) {
      console.error('Failed to get media:', err);
      this.events.onError?.('media_error', 'Failed to access microphone');
    }
  }

  private async handleOffer(sdp: string) {
    if (!this.pc) {
      await this.startWebRTC(false);
    }

    await this.pc!.setRemoteDescription({ type: 'offer', sdp });
    const answer = await this.pc!.createAnswer();
    await this.pc!.setLocalDescription(answer);

    this.send('webrtc.answer', {
      session_id: this.sessionId,
      sdp: answer.sdp
    });
  }

  private send(type: string, payload: Record<string, unknown>) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type, payload }));
    }
  }

  private cleanup() {
    this.localStream?.getTracks().forEach(track => track.stop());
    this.localStream = null;
    this.pc?.close();
    this.pc = null;
    this.sessionId = null;
    this.setState('idle');
  }
}
