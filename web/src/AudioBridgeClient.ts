export interface SignalMessage {
  type: string;
  room?: string;
  client_id?: string;
  sdp?: string;
  candidate?: string;
  data?: string;      // For screenshot base64 data
  target_id?: string; // Target peer for screenshot
}

export type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'failed';

export interface AudioBridgeCallbacks {
  onConnectionStateChange?: (state: ConnectionState) => void;
  onPeerJoined?: (peerId: string) => void;
  onPeerLeft?: (peerId: string) => void;
  onAudioTrack?: (peerId: string, track: MediaStreamTrack) => void;
  onError?: (error: string) => void;
  onScreenShareStateChange?: (isSharing: boolean) => void;
}

export class AudioBridgeClient {
  private ws: WebSocket | null = null;
  private pc: RTCPeerConnection | null = null;
  private localStream: MediaStream | null = null;
  private callbacks: AudioBridgeCallbacks = {};
  private clientId: string;
  private serverUrl: string;

  // Screen sharing
  private screenStream: MediaStream | null = null;
  private screenshotInterval: number | null = null;
  private screenshotCanvas: HTMLCanvasElement | null = null;
  private screenshotVideo: HTMLVideoElement | null = null;

  constructor(clientId: string, serverUrl: string = 'ws://localhost:8080/ws') {
    this.clientId = clientId;
    this.serverUrl = serverUrl;
  }

  setCallbacks(callbacks: AudioBridgeCallbacks) {
    this.callbacks = callbacks;
  }

  async connect(room: string): Promise<void> {
    this.callbacks.onConnectionStateChange?.('connecting');

    try {
      // Get microphone access
      this.localStream = await navigator.mediaDevices.getUserMedia({
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
        },
        video: false,
      });

      // Connect WebSocket
      this.ws = new WebSocket(this.serverUrl);

      await new Promise<void>((resolve, reject) => {
        this.ws!.onopen = () => resolve();
        this.ws!.onerror = () => reject(new Error('WebSocket connection failed'));
        setTimeout(() => reject(new Error('WebSocket connection timeout')), 5000);
      });

      // Create PeerConnection
      this.pc = new RTCPeerConnection({
        iceServers: [{ urls: 'stun:stun.l.google.com:19302' }],
      });

      // Add local audio track
      this.localStream.getAudioTracks().forEach(track => {
        this.pc!.addTrack(track, this.localStream!);
      });

      // Handle ICE candidates
      this.pc.onicecandidate = (event) => {
        if (event.candidate) {
          this.sendMessage({
            type: 'candidate',
            candidate: event.candidate.candidate,
          });
        }
      };

      // Handle incoming tracks
      this.pc.ontrack = (event) => {
        console.log('Received track:', event.track.kind, event.streams);
        if (event.track.kind === 'audio') {
          // Extract peer ID from stream ID (format: stream-peerID)
          const streamId = event.streams[0]?.id || 'unknown';
          const peerId = streamId.startsWith('stream-') ? streamId.slice(7) : streamId;
          this.callbacks.onAudioTrack?.(peerId, event.track);
        }
      };

      // Handle connection state
      this.pc.onconnectionstatechange = () => {
        console.log('Connection state:', this.pc?.connectionState);
        switch (this.pc?.connectionState) {
          case 'connected':
            this.callbacks.onConnectionStateChange?.('connected');
            break;
          case 'failed':
            this.callbacks.onConnectionStateChange?.('failed');
            break;
          case 'disconnected':
          case 'closed':
            this.callbacks.onConnectionStateChange?.('disconnected');
            break;
        }
      };

      // Handle WebSocket messages
      this.ws.onmessage = (event) => {
        const msg: SignalMessage = JSON.parse(event.data);
        this.handleSignalMessage(msg);
      };

      this.ws.onclose = () => {
        this.callbacks.onConnectionStateChange?.('disconnected');
      };

      // Join the room
      this.sendMessage({
        type: 'join',
        room: room,
        client_id: this.clientId,
      });

    } catch (error) {
      this.callbacks.onError?.(error instanceof Error ? error.message : 'Connection failed');
      this.callbacks.onConnectionStateChange?.('failed');
      throw error;
    }
  }

  private async handleSignalMessage(msg: SignalMessage) {
    console.log('Received signal:', msg.type);

    switch (msg.type) {
      case 'offer':
        await this.handleOffer(msg);
        break;
      case 'answer':
        await this.handleAnswer(msg);
        break;
      case 'candidate':
        await this.handleCandidate(msg);
        break;
      case 'peer_joined':
        this.callbacks.onPeerJoined?.(msg.client_id || 'unknown');
        break;
      case 'peer_left':
        this.callbacks.onPeerLeft?.(msg.client_id || 'unknown');
        break;
    }
  }

  private async handleOffer(msg: SignalMessage) {
    if (!this.pc || !msg.sdp) return;

    try {
      await this.pc.setRemoteDescription({
        type: 'offer',
        sdp: msg.sdp,
      });

      const answer = await this.pc.createAnswer();
      await this.pc.setLocalDescription(answer);

      this.sendMessage({
        type: 'answer',
        sdp: answer.sdp,
      });
    } catch (error) {
      console.error('Failed to handle offer:', error);
    }
  }

  private async handleAnswer(msg: SignalMessage) {
    if (!this.pc || !msg.sdp) return;

    try {
      await this.pc.setRemoteDescription({
        type: 'answer',
        sdp: msg.sdp,
      });
    } catch (error) {
      console.error('Failed to handle answer:', error);
    }
  }

  private async handleCandidate(msg: SignalMessage) {
    if (!this.pc || !msg.candidate) return;

    try {
      await this.pc.addIceCandidate({
        candidate: msg.candidate,
      });
    } catch (error) {
      console.error('Failed to add ICE candidate:', error);
    }
  }

  private sendMessage(msg: SignalMessage) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    }
  }

  disconnect() {
    this.localStream?.getTracks().forEach(track => track.stop());
    this.pc?.close();
    this.ws?.close();
    this.localStream = null;
    this.pc = null;
    this.ws = null;
    this.callbacks.onConnectionStateChange?.('disconnected');
  }

  // Mute/unmute microphone
  setMuted(muted: boolean) {
    this.localStream?.getAudioTracks().forEach(track => {
      track.enabled = !muted;
    });
  }

  // Start screen sharing and send periodic screenshots to target peer
  async startScreenShare(targetPeerId: string, intervalMs: number = 2000): Promise<void> {
    try {
      // Request screen capture
      this.screenStream = await navigator.mediaDevices.getDisplayMedia({
        video: {
          width: { max: 1920 },
          height: { max: 1080 },
        },
        audio: false,
      });

      // Create video element to capture frames
      this.screenshotVideo = document.createElement('video');
      this.screenshotVideo.srcObject = this.screenStream;
      this.screenshotVideo.muted = true;
      await this.screenshotVideo.play();

      // Create canvas for screenshot capture
      this.screenshotCanvas = document.createElement('canvas');

      // Handle stream end (user clicks "Stop sharing" in browser UI)
      this.screenStream.getVideoTracks()[0].addEventListener('ended', () => {
        this.stopScreenShare();
      });

      // Start periodic screenshot capture
      this.screenshotInterval = window.setInterval(() => {
        this.captureAndSendScreenshot(targetPeerId);
      }, intervalMs);

      // Send initial screenshot immediately
      this.captureAndSendScreenshot(targetPeerId);

      this.callbacks.onScreenShareStateChange?.(true);
      console.log('Screen sharing started, sending to:', targetPeerId);
    } catch (error) {
      console.error('Failed to start screen sharing:', error);
      this.callbacks.onError?.(error instanceof Error ? error.message : 'Screen share failed');
      throw error;
    }
  }

  // Stop screen sharing
  stopScreenShare() {
    if (this.screenshotInterval !== null) {
      clearInterval(this.screenshotInterval);
      this.screenshotInterval = null;
    }

    if (this.screenStream) {
      this.screenStream.getTracks().forEach(track => track.stop());
      this.screenStream = null;
    }

    if (this.screenshotVideo) {
      this.screenshotVideo.srcObject = null;
      this.screenshotVideo = null;
    }

    this.screenshotCanvas = null;

    this.callbacks.onScreenShareStateChange?.(false);
    console.log('Screen sharing stopped');
  }

  // Capture current screen frame and send to target peer
  private captureAndSendScreenshot(targetPeerId: string) {
    if (!this.screenshotVideo || !this.screenshotCanvas || !this.screenStream) {
      return;
    }

    const video = this.screenshotVideo;
    const canvas = this.screenshotCanvas;

    // Set canvas size to video dimensions (scaled down if needed)
    const maxWidth = 1920;
    const maxHeight = 1080;
    let width = video.videoWidth;
    let height = video.videoHeight;

    if (width > maxWidth || height > maxHeight) {
      const scale = Math.min(maxWidth / width, maxHeight / height);
      width = Math.floor(width * scale);
      height = Math.floor(height * scale);
    }

    canvas.width = width;
    canvas.height = height;

    // Draw video frame to canvas
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    ctx.drawImage(video, 0, 0, width, height);

    // Convert to JPEG base64 (0.7 quality for reasonable size)
    const dataUrl = canvas.toDataURL('image/jpeg', 0.7);
    const base64Data = dataUrl.split(',')[1]; // Remove "data:image/jpeg;base64," prefix

    // Send via WebSocket
    this.sendMessage({
      type: 'screenshot',
      data: base64Data,
      target_id: targetPeerId,
    });

    console.log(`Screenshot sent to ${targetPeerId} (${Math.round(base64Data.length / 1024)}KB)`);
  }

  // Check if screen sharing is active
  isScreenSharing(): boolean {
    return this.screenStream !== null;
  }
}
