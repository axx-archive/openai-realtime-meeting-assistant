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
  const [freshness, setFreshness] = useState<HomeCanvasSnapshot['freshness']>('loading');
  const [refreshing, setRefreshing] = useState(false);
  const [refreshError, setRefreshError] = useState('');
  const generationRef = useRef(0);

  const load = useCallback(async () => {
    const generation = ++generationRef.current;
    if (!sessionToken) {
      setSnapshot(EMPTY);
      setFreshness('loading');
      setRefreshError('');
      return;
    }
    setRefreshing(true);
    try {
      const response = await api.home(sessionToken);
      if (generation !== generationRef.current) return;
      if (!validHomeSnapshot(response.home)) throw new Error('Home returned an invalid snapshot.');
      setSnapshot(response.home);
      setFreshness('current');
      setRefreshError('');
    } catch (error) {
      if (generation !== generationRef.current) return;
      // Home is an authorization-filtered projection. Any failed refresh may
      // coincide with a membership, audience, or source revocation, so never
      // retain previously authorized titles, previews, starters, or routes.
      setSnapshot(EMPTY);
      if (error instanceof BonfireApiError && [401, 403].includes(error.status)) {
        setFreshness('loading');
        setRefreshError('');
      } else {
        setFreshness('stale');
        setRefreshError('Home could not refresh. Your conversations are still available in Work.');
      }
    } finally {
      if (generation === generationRef.current) setRefreshing(false);
    }
  }, [sessionToken]);

  useFocusEffect(
    useCallback(() => {
      void load();
    }, [load]),
  );

  useEffect(() => {
    if (!['rooms', 'participants', 'notification', 'notification_backlog', 'chat_thread'].includes(office.event ?? '')) return;
    void load();
  }, [load, office.event, office.version]);

  return {
    continuity: snapshot.items,
    // Canvas only mounts in the signed-in shell. The four non-personal cards
    // can therefore reserve their geometry immediately while auth restoration
    // and the authorized recommendation snapshot complete.
    starters: snapshot.starters.length !== HOME_STARTER_IDS.length
      ? HOME_STARTER_SHELLS
      : snapshot.starters,
    startersReady: snapshot.starters.length === HOME_STARTER_IDS.length && freshness === 'current',
    allClear: snapshot.allClear,
    freshness,
    refreshing,
    refreshError,
    refresh: load,
  };
}
