import React, {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type PropsWithChildren,
} from 'react';
import { AppState } from 'react-native';
import { useAuth } from '../auth/AuthContext';
import { API_BASE_URL, NATIVE_CLIENT_HEADER } from '../config';

type OfficeEventState = {
  event: string | null;
  version: number;
  connected: boolean;
};

const OfficeEventsContext = createContext<OfficeEventState>({
  event: null,
  version: 0,
  connected: false,
});

type NativeWebSocketConstructor = new (
  uri: string,
  protocols?: string | string[] | null,
  options?: { headers: Record<string, string> },
) => WebSocket;

function officeSocketURL(): string {
  const url = new URL('/websocket', API_BASE_URL);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  return url.toString();
}

/**
 * Keeps the native shell on the same event stream as the web OS without
 * taking a room seat. The server's office socket is read-only and carries
 * safe board, room, chat, memory, meeting, and notification invalidations.
 */
export function OfficeEventsProvider({ children }: PropsWithChildren) {
  const { sessionToken, user } = useAuth();
  const [state, setState] = useState<OfficeEventState>({
    event: null,
    version: 0,
    connected: false,
  });
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const socketRef = useRef<WebSocket | null>(null);
  const attempts = useRef(0);

  useEffect(() => {
    let disposed = false;
    let heartbeat: ReturnType<typeof setInterval> | null = null;

    const clearReconnect = () => {
      if (reconnectTimer.current) clearTimeout(reconnectTimer.current);
      reconnectTimer.current = null;
    };

    const close = () => {
      clearReconnect();
      if (heartbeat) clearInterval(heartbeat);
      heartbeat = null;
      const socket = socketRef.current;
      socketRef.current = null;
      socket?.close();
      setState((current) => ({ ...current, connected: false }));
    };

    if (!sessionToken || !user) {
      close();
      return close;
    }

    const connect = () => {
      if (disposed || AppState.currentState !== 'active') return;
      const current = socketRef.current;
      if (current && (current.readyState === WebSocket.OPEN || current.readyState === WebSocket.CONNECTING)) return;

      const NativeWebSocket = WebSocket as unknown as NativeWebSocketConstructor;
      const socket = new NativeWebSocket(officeSocketURL(), [], {
        headers: {
          Authorization: `Bearer ${sessionToken}`,
          'X-Bonfire-Client': NATIVE_CLIENT_HEADER,
        },
      });
      socketRef.current = socket;

      socket.onopen = () => {
        if (disposed || socketRef.current !== socket) return;
        attempts.current = 0;
        socket.send(JSON.stringify({ event: 'office', data: '{}' }));
        setState((currentState) => ({ ...currentState, connected: true }));
        if (heartbeat) clearInterval(heartbeat);
        heartbeat = setInterval(() => {
          if (socket.readyState === WebSocket.OPEN) {
            socket.send(JSON.stringify({ event: 'office_ping', data: '{}' }));
          }
        }, 30_000);
      };

      socket.onmessage = (message) => {
        if (disposed || socketRef.current !== socket) return;
        try {
          const envelope = JSON.parse(String(message.data)) as { event?: string; data?: string };
          if (envelope.event !== 'kanban' || typeof envelope.data !== 'string') return;
          const nested = JSON.parse(envelope.data) as { event?: string };
          if (!nested.event) return;
          setState((currentState) => ({
            event: nested.event ?? null,
            version: currentState.version + 1,
            connected: true,
          }));
        } catch {
          // A malformed or future event must not destabilize the native shell.
        }
      };

      socket.onerror = () => socket.close();
      socket.onclose = () => {
        if (socketRef.current === socket) socketRef.current = null;
        if (heartbeat) clearInterval(heartbeat);
        heartbeat = null;
        setState((currentState) => ({ ...currentState, connected: false }));
        if (disposed) return;
        const delay = Math.min(1_000 * 2 ** attempts.current, 30_000);
        attempts.current += 1;
        reconnectTimer.current = setTimeout(connect, delay);
      };
    };

    connect();
    const subscription = AppState.addEventListener('change', (nextState) => {
      if (nextState === 'active') connect();
      else close();
    });

    return () => {
      disposed = true;
      subscription.remove();
      close();
    };
  }, [sessionToken, user]);

  const value = useMemo(() => state, [state]);
  return <OfficeEventsContext.Provider value={value}>{children}</OfficeEventsContext.Provider>;
}

export function useOfficeEvents(): OfficeEventState {
  return useContext(OfficeEventsContext);
}
