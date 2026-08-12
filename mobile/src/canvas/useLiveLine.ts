import { useCallback, useEffect, useRef, useState } from 'react';
import { useFocusEffect } from '@react-navigation/native';
import { api, BonfireApiError } from '../api/client';
import type { HomeItem, HomeSnapshot, HomeStarter } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';

export type HomeCanvasSnapshot = {
  continuity: HomeItem[];
  starters: HomeStarter[];
  startersReady: boolean;
  allClear: boolean;
  freshness: 'loading' | 'current' | 'stale';
  refreshing: boolean;
  refreshError: string;
  refresh: () => Promise<void>;
};

const EMPTY: HomeSnapshot = {
  version: 'home-v2',
  generatedAt: '',
  items: [],
  starters: [],
  allClear: false,
};

const HOME_STARTER_IDS = ['continue', 'explore', 'create', 'challenge'] as const;
const HOME_STARTER_SHELLS: HomeStarter[] = [
  { id: 'continue', label: 'Continue', detail: '', suggestions: [] },
  { id: 'explore', label: 'Explore', detail: '', suggestions: [] },
  { id: 'create', label: 'Create', detail: '', suggestions: [] },
  { id: 'challenge', label: 'Challenge', detail: '', suggestions: [] },
];

function validHomeStarterDestination(value: unknown): boolean {
  if (!value || typeof value !== 'object') return false;
  const destination = value as Record<string, unknown>;
  if (destination.route === 'new-private') return true;
  if (destination.route === 'thread') return typeof destination.threadId === 'string' && Boolean(destination.threadId.trim());
  return false;
}

function validHomeStarter(value: unknown, expectedID: string): boolean {
  if (!value || typeof value !== 'object') return false;
  const starter = value as Record<string, unknown>;
  return starter.id === expectedID
    && typeof starter.label === 'string'
    && typeof starter.detail === 'string'
    && Array.isArray(starter.suggestions)
    && starter.suggestions.length > 0
    && starter.suggestions.every((suggestion) => {
      if (!suggestion || typeof suggestion !== 'object') return false;
      const candidate = suggestion as Record<string, unknown>;
      return typeof candidate.id === 'string'
        && typeof candidate.text === 'string'
        && Boolean(candidate.text.trim())
        && typeof candidate.whyThis === 'string'
        && Boolean(candidate.whyThis.trim())
        && validHomeStarterDestination(candidate.destination);
    });
}

function validHomeSnapshot(value: unknown): value is HomeSnapshot {
  if (!value || typeof value !== 'object') return false;
  const snapshot = value as Partial<HomeSnapshot>;
  return snapshot.version === 'home-v2'
    && typeof snapshot.generatedAt === 'string'
    && Array.isArray(snapshot.items)
    && Array.isArray(snapshot.starters)
    && snapshot.starters.length === HOME_STARTER_IDS.length
    && HOME_STARTER_IDS.every((id, index) => validHomeStarter(snapshot.starters?.[index], id))
    && typeof snapshot.allClear === 'boolean';
}

export function useHomeCanvas(): HomeCanvasSnapshot {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const [snapshot, setSnapshot] = useState<HomeSnapshot>(EMPTY);
  const [snapshotSessionToken, setSnapshotSessionToken] = useState('');
  const [freshness, setFreshness] = useState<HomeCanvasSnapshot['freshness']>('loading');
  const [refreshing, setRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState('');
  const generationRef = useRef(0);
  const sessionTokenRef = useRef(sessionToken);
  const requestRef = useRef<{ sessionToken: string; promise: Promise<void>; refreshQueued: boolean } | null>(null);
  sessionTokenRef.current = sessionToken;

  const load = useCallback(async (refreshAfterCurrent = false) => {
    const activeRequest = requestRef.current;
    if (sessionToken && activeRequest?.sessionToken === sessionToken) {
      if (refreshAfterCurrent) activeRequest.refreshQueued = true;
      return activeRequest.promise;
    }
    const generation = ++generationRef.current;
    if (!sessionToken) {
      requestRef.current = null;
      setSnapshot(EMPTY);
      setSnapshotSessionToken('');
      setFreshness('loading');
      setRefreshError('');
      return;
    }
    setRefreshing(true);
    const request = (async () => {
      try {
        const response = await api.home(sessionToken);
        if (generation !== generationRef.current || sessionTokenRef.current !== sessionToken) return;
        if (!validHomeSnapshot(response.home)) throw new Error('Home returned an invalid snapshot.');
        setSnapshot(response.home);
        setSnapshotSessionToken(sessionToken);
        setFreshness('current');
        setRefreshError('');
      } catch (error) {
        if (generation !== generationRef.current || sessionTokenRef.current !== sessionToken) return;
        // Home is an authorization-filtered projection. Any failed refresh may
        // coincide with a membership, audience, or source revocation, so never
        // retain previously authorized titles, previews, starters, or routes.
        setSnapshot(EMPTY);
        setSnapshotSessionToken(sessionToken);
        if (error instanceof BonfireApiError && [401, 403].includes(error.status)) {
          setFreshness('loading');
          setRefreshError('');
        } else {
          setFreshness('stale');
          setRefreshError('Home could not refresh. Your conversations are still available in Work.');
        }
      } finally {
        if (generation === generationRef.current && sessionTokenRef.current === sessionToken) setRefreshing(false);
      }
    })();
    const currentRequest = { sessionToken, promise: request, refreshQueued: false };
    requestRef.current = currentRequest;
    try {
      return await request;
    } finally {
      if (requestRef.current !== currentRequest) return;
      requestRef.current = null;
      if (currentRequest.refreshQueued && sessionTokenRef.current === sessionToken) void load();
    }
  }, [sessionToken]);

  useFocusEffect(
    useCallback(() => {
      void load();
    }, [load]),
  );

  useEffect(() => {
    if (!['rooms', 'participants', 'notification', 'notification_backlog', 'chat_thread'].includes(office.event ?? '')) return;
    void load(true);
  }, [load, office.event, office.version]);

  // State updates run after render. Bind every personalized projection to the
  // exact session that authorized it so the first A -> B render can only show
  // the non-personal four-card shell, never A's titles or destinations.
  const authorizedSnapshot = snapshotSessionToken === sessionToken ? snapshot : EMPTY;

  return {
    continuity: authorizedSnapshot.items,
    // Canvas only mounts in the signed-in shell. The four non-personal cards
    // can therefore reserve their geometry immediately while auth restoration
    // and the authorized recommendation snapshot complete.
    starters: authorizedSnapshot.starters.length !== HOME_STARTER_IDS.length
      ? HOME_STARTER_SHELLS
      : authorizedSnapshot.starters,
    startersReady: authorizedSnapshot.starters.length === HOME_STARTER_IDS.length && freshness === 'current',
    allClear: authorizedSnapshot.allClear,
    freshness,
    refreshing,
    refreshError,
    refresh: load,
  };
}
