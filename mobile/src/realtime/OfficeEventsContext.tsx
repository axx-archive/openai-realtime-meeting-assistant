import React, {
  createContext,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type PropsWithChildren,
} from 'react';
import { AppState } from 'react-native';
import { useAuth } from '../auth/AuthContext';
import { API_BASE_URL, NATIVE_CLIENT_HEADER } from '../config';
import { audioFocusRuntime } from './audioFocusRuntime';
import { encodeOfficeCommand, parseOfficeEventEnvelope } from './officeEventProtocol';

type OfficeEventState = {
  event: string | null;
  data: unknown;
  version: number;
  connected: boolean;
  send: (event: string, data: unknown) => boolean;
};

type ScopedOfficeEventState = OfficeEventState & {
  authScope: string | null;
};

const unavailableSend = () => false;
const OFFICE_HEARTBEAT_INTERVAL_MS = 30_000;
const OFFICE_SILENCE_TIMEOUT_MS = 75_000;

const OfficeEventsContext = createContext<OfficeEventState>({
  event: null,
  data: null,
  version: 0,
  connected: false,
  send: unavailableSend,
});

function emptyOfficeEventState(authScope: string | null): ScopedOfficeEventState {
  return {
    authScope,
    event: null,
    data: null,
    version: 0,
    connected: false,
    send: unavailableSend,
  };
}

function officeAuthScope(sessionToken: string | null, email: string | undefined): string | null {
  const identity = email?.trim().toLowerCase();
  return sessionToken && identity ? `${identity}\u0000${sessionToken}` : null;
}

const officeControlRuntime = {
  sessionToken: null as string | null,
  live: false,
  generation: 0,
  reconnectEligible: false,
};
const officeControlListeners = new Set<() => void>();

function notifyOfficeControlListeners(): void {
  officeControlListeners.forEach((listener) => listener());
}

function closePersonalRealtimeForControlLoss(): void {
  if (audioFocusRuntime.mode !== 'personal_realtime') return;
  void audioFocusRuntime.forceClose('forced_close').catch(() => {
    // The coordinator invalidates the generation before native cleanup.
  });
}

function commitOfficeControlSession(sessionToken: string | null): void {
  if (officeControlRuntime.sessionToken === sessionToken && !officeControlRuntime.live) return;
  officeControlRuntime.sessionToken = sessionToken;
  officeControlRuntime.live = false;
  officeControlRuntime.reconnectEligible = false;
  officeControlRuntime.generation += 1;
  notifyOfficeControlListeners();
  closePersonalRealtimeForControlLoss();
}

function markOfficeControlLive(sessionToken: string): void {
  if (officeControlRuntime.sessionToken !== sessionToken || officeControlRuntime.live) return;
  officeControlRuntime.live = true;
  officeControlRuntime.reconnectEligible = false;
  officeControlRuntime.generation += 1;
  notifyOfficeControlListeners();
}

function markOfficeControlDisconnected(sessionToken: string, reconnectEligible = true): void {
  if (officeControlRuntime.sessionToken !== sessionToken) return;
  if (officeControlRuntime.live || officeControlRuntime.reconnectEligible !== reconnectEligible) {
    officeControlRuntime.live = false;
    officeControlRuntime.reconnectEligible = reconnectEligible;
    officeControlRuntime.generation += 1;
    notifyOfficeControlListeners();
  }
  // A transient office-socket reconnect is not an authority change. The
  // private Realtime offer and every tool request remain independently bound
  // to the exact authenticated session on the server, so tearing down the
  // audio peer here cancels an in-flight answer and makes Scout appear unable
  // to answer. Session replacement/logout still closes synchronously through
  // commitOfficeControlSession; relationship-memory drift has its own explicit
  // close below. Only a non-reconnectable control loss is terminal here.
  if (!reconnectEligible) closePersonalRealtimeForControlLoss();
}

export function officeControlChannelIsLive(sessionToken: string | null): boolean {
  return Boolean(
    sessionToken
    && officeControlRuntime.sessionToken === sessionToken
    && officeControlRuntime.live,
  );
}

export function officeControlChannelSnapshot(sessionToken: string | null): {
  live: boolean;
  generation: number;
  reconnectEligible: boolean;
} {
  const scoped = Boolean(sessionToken && officeControlRuntime.sessionToken === sessionToken);
  return {
    live: scoped && officeControlRuntime.live,
    generation: officeControlRuntime.generation,
    reconnectEligible: scoped && officeControlRuntime.reconnectEligible,
  };
}

export async function waitForOfficeControlChannel(
  sessionToken: string,
  isCurrent: () => boolean,
  timeoutMs = 6_000,
): Promise<boolean> {
  if (officeControlChannelIsLive(sessionToken)) return true;
  return new Promise((resolve) => {
    let settled = false;
    const finish = (value: boolean) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      clearInterval(cancellationPoll);
      officeControlListeners.delete(check);
      resolve(value);
    };
    const check = () => {
      if (!isCurrent()) finish(false);
      else if (officeControlChannelIsLive(sessionToken)) finish(true);
    };
    const timeout = setTimeout(() => finish(false), timeoutMs);
    const cancellationPoll = setInterval(check, 100);
    officeControlListeners.add(check);
    check();
  });
}

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
  const authScope = officeAuthScope(sessionToken, user?.email);
  const [state, setState] = useState<ScopedOfficeEventState>(() => emptyOfficeEventState(null));
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const socketRef = useRef<WebSocket | null>(null);
  const socketScopeRef = useRef<string | null>(null);
  const currentAuthScopeRef = useRef<string | null>(authScope);
  const attempts = useRef(0);

  useLayoutEffect(() => {
    currentAuthScopeRef.current = authScope;
    commitOfficeControlSession(sessionToken);
  }, [authScope, sessionToken]);

  useEffect(() => {
    const effectScope = authScope;
    let disposed = false;
    let heartbeat: ReturnType<typeof setInterval> | null = null;
    let lastFrameAt = 0;

    setState((current) => (
      currentAuthScopeRef.current !== effectScope || current.authScope === effectScope
        ? current
        : emptyOfficeEventState(effectScope)
    ));

    const clearReconnect = () => {
      if (reconnectTimer.current) clearTimeout(reconnectTimer.current);
      reconnectTimer.current = null;
    };

    const close = () => {
      if (sessionToken) markOfficeControlDisconnected(sessionToken, false);
      clearReconnect();
      if (heartbeat) clearInterval(heartbeat);
      heartbeat = null;
      lastFrameAt = 0;
      const ownsSocket = socketScopeRef.current === effectScope;
      const socket = ownsSocket ? socketRef.current : null;
      if (ownsSocket) {
        socketRef.current = null;
        socketScopeRef.current = null;
      }
      socket?.close();
      if (currentAuthScopeRef.current === effectScope) {
        setState((current) => (
          currentAuthScopeRef.current !== effectScope
            ? current
            : current.authScope === effectScope
            ? { ...current, connected: false }
            : emptyOfficeEventState(effectScope)
        ));
      }
    };

    if (!effectScope || !sessionToken) {
      close();
      return close;
    }

    const connect = () => {
      if (
        disposed
        || currentAuthScopeRef.current !== effectScope
        || AppState.currentState !== 'active'
      ) return;
      const current = socketRef.current;
      if (
        socketScopeRef.current === effectScope
        && current
        && (current.readyState === WebSocket.OPEN || current.readyState === WebSocket.CONNECTING)
      ) return;

      const NativeWebSocket = WebSocket as unknown as NativeWebSocketConstructor;
      const socket = new NativeWebSocket(officeSocketURL(), [], {
        headers: {
          Authorization: `Bearer ${sessionToken}`,
          'X-Bonfire-Client': NATIVE_CLIENT_HEADER,
        },
      });
      socketRef.current = socket;
      socketScopeRef.current = effectScope;

      socket.onopen = () => {
        if (
          disposed
          || currentAuthScopeRef.current !== effectScope
          || socketScopeRef.current !== effectScope
          || socketRef.current !== socket
        ) return;
        attempts.current = 0;
        lastFrameAt = Date.now();
        socket.send(JSON.stringify({ event: 'office', data: '{}' }));
        markOfficeControlLive(sessionToken);
        setState((currentState) => (
          currentAuthScopeRef.current !== effectScope
            ? currentState
            : currentState.authScope === effectScope
            ? { ...currentState, connected: true }
            : { ...emptyOfficeEventState(effectScope), connected: true }
        ));
        if (heartbeat) clearInterval(heartbeat);
        heartbeat = setInterval(() => {
          if (
            currentAuthScopeRef.current === effectScope
            && socketScopeRef.current === effectScope
            && socketRef.current === socket
            && socket.readyState === WebSocket.OPEN
          ) {
            if (lastFrameAt && Date.now() - lastFrameAt > OFFICE_SILENCE_TIMEOUT_MS) {
              // OPEN can lie after a NAT rebind, sleep, or proxy idle kill.
              // Fence personal Realtime before asking the stale pipe to close;
              // onclose owns the reconnect backoff.
              markOfficeControlDisconnected(sessionToken);
              socket.close();
              return;
            }
            try {
              socket.send(JSON.stringify({ event: 'office_ping', data: '{}' }));
            } catch {
              markOfficeControlDisconnected(sessionToken);
              socket.close();
            }
          }
        }, OFFICE_HEARTBEAT_INTERVAL_MS);
      };

      socket.onmessage = (message) => {
        if (
          disposed
          || currentAuthScopeRef.current !== effectScope
          || socketScopeRef.current !== effectScope
          || socketRef.current !== socket
        ) return;
        // Every frame proves liveness, including the server's top-level
        // office_pong (which is intentionally not a kanban event).
        lastFrameAt = Date.now();
        const nested = parseOfficeEventEnvelope(String(message.data));
        if (!nested) return;
        if (nested.event === 'relationship_memory_changed' && audioFocusRuntime.mode === 'personal_realtime') {
          // Realtime instructions are immutable for the life of a provider
          // session. A cross-device correction/revoke therefore closes the
          // stale session immediately; the next call is minted from the new
          // authoritative memory projection.
          void audioFocusRuntime.forceClose('forced_close').catch(() => {
            // The focus coordinator already fences the generation before
            // invoking device cleanup, so a callback error cannot revive it.
          });
        }
        setState((currentState) => {
          if (currentAuthScopeRef.current !== effectScope) return currentState;
          return {
            ...(
              currentState.authScope === effectScope
                ? currentState
                : emptyOfficeEventState(effectScope)
            ),
            authScope: effectScope,
            event: nested.event,
            data: nested.data,
            version: currentState.authScope === effectScope ? currentState.version + 1 : 1,
            connected: true,
          };
        });
      };

      socket.onerror = () => {
        if (socketScopeRef.current !== effectScope || socketRef.current !== socket) return;
        markOfficeControlDisconnected(sessionToken);
        socket.close();
      };
      socket.onclose = () => {
        // A closing socket can outlive a background/foreground transition that
        // already installed a replacement. Only the exact current socket may
        // revoke control authority or schedule its reconnect.
        if (socketScopeRef.current !== effectScope || socketRef.current !== socket) return;
        markOfficeControlDisconnected(sessionToken);
        socketRef.current = null;
        socketScopeRef.current = null;
        if (heartbeat) clearInterval(heartbeat);
        heartbeat = null;
        lastFrameAt = 0;
        if (disposed || currentAuthScopeRef.current !== effectScope) return;
        setState((currentState) => (
          currentAuthScopeRef.current !== effectScope
            ? currentState
            : currentState.authScope === effectScope
            ? { ...currentState, connected: false }
            : emptyOfficeEventState(effectScope)
        ));
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
  }, [authScope, sessionToken]);

  const send = React.useCallback((event: string, data: unknown) => {
    const socket = socketRef.current;
    const currentScope = currentAuthScopeRef.current;
    const encoded = encodeOfficeCommand(event, data);
    if (
      !encoded
      || !currentScope
      || socketScopeRef.current !== currentScope
      || !socket
      || socket.readyState !== WebSocket.OPEN
    ) return false;
    try {
      socket.send(encoded);
      return true;
    } catch {
      return false;
    }
  }, []);

  const value = useMemo<OfficeEventState>(() => {
    // This guard applies during the render that observes new auth, before the
    // old effect's cleanup has run. Prior-account event/data is therefore never
    // exposed through context, even for one committed frame.
    if (!authScope || state.authScope !== authScope) {
      return emptyOfficeEventState(null);
    }
    return {
      event: state.event,
      data: state.data,
      version: state.version,
      connected: state.connected,
      send,
    };
  }, [authScope, send, state]);
  return <OfficeEventsContext.Provider value={value}>{children}</OfficeEventsContext.Provider>;
}

export function useOfficeEvents(): OfficeEventState {
  return useContext(OfficeEventsContext);
}
