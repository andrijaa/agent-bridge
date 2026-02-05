import { useState, useRef, useCallback, useEffect } from 'react';
import { AudioBridgeClient, ConnectionState } from './AudioBridgeClient';
import { PersonaSelector, PersonaKey } from './PersonaSelector';

function App() {
  const [connectionState, setConnectionState] = useState<ConnectionState>('disconnected');
  const [room, setRoom] = useState('test');
  const [clientId, setClientId] = useState(`web-${Math.random().toString(36).slice(2, 8)}`);
  const [isMuted, setIsMuted] = useState(false);
  const [isScreenSharing, setIsScreenSharing] = useState(false);
  const [peers, setPeers] = useState<string[]>([]);
  const [logs, setLogs] = useState<string[]>([]);
  const [targetPeerId, setTargetPeerId] = useState('ai-agent');
  const [selectedPersona, setSelectedPersona] = useState<PersonaKey>('assistant');
  const clientRef = useRef<AudioBridgeClient | null>(null);
  const audioRefs = useRef<Map<string, HTMLAudioElement>>(new Map());

  const addLog = useCallback((message: string) => {
    const timestamp = new Date().toLocaleTimeString();
    setLogs(prev => [...prev.slice(-50), `[${timestamp}] ${message}`]);
  }, []);

  const handleConnect = async () => {
    if (connectionState === 'connected') {
      clientRef.current?.disconnect();
      clientRef.current = null;
      setPeers([]);
      return;
    }

    const client = new AudioBridgeClient(clientId);
    clientRef.current = client;

    client.setCallbacks({
      onConnectionStateChange: (state) => {
        setConnectionState(state);
        addLog(`Connection state: ${state}`);
      },
      onPeerJoined: (peerId) => {
        setPeers(prev => [...prev, peerId]);
        addLog(`Peer joined: ${peerId}`);
      },
      onPeerLeft: (peerId) => {
        setPeers(prev => prev.filter(p => p !== peerId));
        addLog(`Peer left: ${peerId}`);
        // Clean up audio element
        const audio = audioRefs.current.get(peerId);
        if (audio) {
          audio.srcObject = null;
          audioRefs.current.delete(peerId);
        }
      },
      onAudioTrack: (peerId, track) => {
        addLog(`Received audio from: ${peerId}`);
        // Create audio element to play the track
        let audio = audioRefs.current.get(peerId);
        if (!audio) {
          audio = new Audio();
          audio.autoplay = true;
          audioRefs.current.set(peerId, audio);
        }
        audio.srcObject = new MediaStream([track]);
      },
      onError: (error) => {
        addLog(`Error: ${error}`);
      },
      onScreenShareStateChange: (sharing) => {
        setIsScreenSharing(sharing);
        addLog(sharing ? 'Screen sharing started' : 'Screen sharing stopped');
      },
    });

    try {
      await client.connect(room);
    } catch (error) {
      addLog(`Failed to connect: ${error}`);
    }
  };

  const handleMuteToggle = () => {
    const newMuted = !isMuted;
    setIsMuted(newMuted);
    clientRef.current?.setMuted(newMuted);
    addLog(newMuted ? 'Microphone muted' : 'Microphone unmuted');
  };

  const handleScreenShareToggle = async () => {
    if (isScreenSharing) {
      clientRef.current?.stopScreenShare();
    } else {
      try {
        await clientRef.current?.startScreenShare(targetPeerId, 2000);
      } catch (error) {
        addLog(`Screen share error: ${error}`);
      }
    }
  };

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      clientRef.current?.disconnect();
    };
  }, []);

  return (
    <div style={styles.container}>
      <h1 style={styles.title}>Audio Bridge</h1>

      <div style={styles.card}>
        <h2 style={styles.cardTitle}>Connection</h2>

        <div style={styles.inputGroup}>
          <label style={styles.label}>Client ID</label>
          <input
            style={styles.input}
            type="text"
            value={clientId}
            onChange={(e) => setClientId(e.target.value)}
            disabled={connectionState === 'connected'}
          />
        </div>

        <div style={styles.inputGroup}>
          <label style={styles.label}>Room</label>
          <input
            style={styles.input}
            type="text"
            value={room}
            onChange={(e) => setRoom(e.target.value)}
            disabled={connectionState === 'connected'}
          />
        </div>

        <div style={styles.buttonGroup}>
          <button
            style={{
              ...styles.button,
              backgroundColor: connectionState === 'connected' ? '#ef4444' : '#22c55e',
            }}
            onClick={handleConnect}
            disabled={connectionState === 'connecting'}
          >
            {connectionState === 'connected' ? 'Disconnect' :
             connectionState === 'connecting' ? 'Connecting...' : 'Connect'}
          </button>

          {connectionState === 'connected' && (
            <button
              style={{
                ...styles.button,
                backgroundColor: isMuted ? '#f59e0b' : '#6b7280',
              }}
              onClick={handleMuteToggle}
            >
              {isMuted ? 'Unmute' : 'Mute'}
            </button>
          )}
        </div>

        {connectionState === 'connected' && (
          <div style={styles.screenShareSection}>
            <div style={styles.inputGroup}>
              <label style={styles.label}>Screenshot Target Peer</label>
              <input
                style={styles.input}
                type="text"
                value={targetPeerId}
                onChange={(e) => setTargetPeerId(e.target.value)}
                disabled={isScreenSharing}
                placeholder="e.g., ai-agent"
              />
            </div>
            <button
              style={{
                ...styles.button,
                backgroundColor: isScreenSharing ? '#ef4444' : '#3b82f6',
              }}
              onClick={handleScreenShareToggle}
            >
              {isScreenSharing ? 'Stop Sharing' : 'Share Screen'}
            </button>
            {isScreenSharing && (
              <p style={styles.shareInfo}>
                Sending screenshots to: {targetPeerId}
              </p>
            )}
          </div>
        )}

        <div style={styles.status}>
          Status: <span style={{
            color: connectionState === 'connected' ? '#22c55e' :
                   connectionState === 'connecting' ? '#f59e0b' : '#6b7280'
          }}>{connectionState}</span>
        </div>
      </div>

      <div style={styles.card}>
        <PersonaSelector
          selectedPersona={selectedPersona}
          onSelectPersona={setSelectedPersona}
          disabled={connectionState === 'connected'}
        />
      </div>

      <div style={styles.card}>
        <h2 style={styles.cardTitle}>Connected Peers ({peers.length})</h2>
        {peers.length === 0 ? (
          <p style={styles.emptyText}>No peers connected</p>
        ) : (
          <ul style={styles.peerList}>
            {peers.map(peer => (
              <li key={peer} style={styles.peerItem}>{peer}</li>
            ))}
          </ul>
        )}
      </div>

      <div style={styles.card}>
        <h2 style={styles.cardTitle}>Logs</h2>
        <div style={styles.logContainer}>
          {logs.map((log, i) => (
            <div key={i} style={styles.logLine}>{log}</div>
          ))}
        </div>
      </div>
    </div>
  );
}

const styles: Record<string, React.CSSProperties> = {
  container: {
    maxWidth: '600px',
    margin: '0 auto',
    padding: '20px',
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif',
  },
  title: {
    textAlign: 'center',
    marginBottom: '24px',
    color: '#1f2937',
  },
  card: {
    backgroundColor: '#fff',
    borderRadius: '8px',
    padding: '20px',
    marginBottom: '16px',
    boxShadow: '0 1px 3px rgba(0,0,0,0.1)',
  },
  cardTitle: {
    margin: '0 0 16px 0',
    fontSize: '18px',
    color: '#374151',
  },
  inputGroup: {
    marginBottom: '12px',
  },
  label: {
    display: 'block',
    marginBottom: '4px',
    fontSize: '14px',
    color: '#6b7280',
  },
  input: {
    width: '100%',
    padding: '8px 12px',
    fontSize: '16px',
    border: '1px solid #d1d5db',
    borderRadius: '6px',
    boxSizing: 'border-box',
  },
  buttonGroup: {
    display: 'flex',
    gap: '8px',
    marginTop: '16px',
  },
  button: {
    flex: 1,
    padding: '12px 16px',
    fontSize: '16px',
    fontWeight: '500',
    color: '#fff',
    border: 'none',
    borderRadius: '6px',
    cursor: 'pointer',
  },
  status: {
    marginTop: '12px',
    fontSize: '14px',
    color: '#6b7280',
  },
  peerList: {
    listStyle: 'none',
    padding: 0,
    margin: 0,
  },
  peerItem: {
    padding: '8px 12px',
    backgroundColor: '#f3f4f6',
    borderRadius: '4px',
    marginBottom: '4px',
  },
  emptyText: {
    color: '#9ca3af',
    fontStyle: 'italic',
  },
  logContainer: {
    maxHeight: '200px',
    overflowY: 'auto',
    backgroundColor: '#1f2937',
    borderRadius: '6px',
    padding: '12px',
  },
  logLine: {
    fontFamily: 'monospace',
    fontSize: '12px',
    color: '#d1d5db',
    marginBottom: '2px',
  },
  screenShareSection: {
    marginTop: '16px',
    paddingTop: '16px',
    borderTop: '1px solid #e5e7eb',
  },
  shareInfo: {
    marginTop: '8px',
    fontSize: '12px',
    color: '#6b7280',
    fontStyle: 'italic',
  },
};

export default App;
