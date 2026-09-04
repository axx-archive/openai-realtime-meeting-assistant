import { useCallback, useEffect, useRef, useState } from 'react';
import { useFocusEffect } from '@react-navigation/native';
import { api } from '../api/client';
import type { StudioProject } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { homeOperatingBrief } from './homeOperatingBrief';

const EMPTY: StudioProject[] = [];

export function useHomeOperatingBrief() {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const [snapshot, setSnapshot] = useState<{ session: string; projects: StudioProject[]; hasMore: boolean } | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const generation = useRef(0);
  const focused = useRef(false);
  const currentSession = useRef(sessionToken);
  currentSession.current = sessionToken;

  const refresh = useCallback(async () => {
    const requestGeneration = ++generation.current;
    if (!sessionToken) {
      setSnapshot(null);
      setError('');
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const response = await api.studioProjects(sessionToken, { limit: 100 });
      if (requestGeneration !== generation.current || currentSession.current !== sessionToken || !focused.current) return;
      if (!response.ok || !Array.isArray(response.projects)) throw new Error('Invalid Work response');
      setSnapshot({ session: sessionToken, projects: response.projects, hasMore: response.hasMore });
      setError('');
    } catch {
      if (requestGeneration !== generation.current || currentSession.current !== sessionToken || !focused.current) return;
      // A failure can coincide with revoked access. Do not keep old titles or
      // actionable project routes after any failed authorization refresh.
      setSnapshot(null);
      setError('Your work could not refresh. Try again to see current decisions and progress.');
    } finally {
      if (requestGeneration === generation.current && currentSession.current === sessionToken && focused.current) setLoading(false);
    }
  }, [sessionToken]);

  useFocusEffect(useCallback(() => {
    focused.current = true;
    void refresh();
    const timer = setInterval(() => { void refresh(); }, 30_000);
    return () => {
      focused.current = false;
      generation.current += 1;
      clearInterval(timer);
    };
  }, [refresh]));

  useEffect(() => {
    if (focused.current && ['chat_thread', 'file', 'action', 'memory', 'notification'].includes(office.event ?? '')) void refresh();
  }, [office.event, office.version, refresh]);

  const authorized = snapshot?.session === sessionToken ? snapshot : null;
  return {
    ...homeOperatingBrief(authorized?.projects ?? EMPTY),
    hasMore: authorized?.hasMore ?? false,
    ready: authorized !== null,
    loading,
    error,
    refresh,
  };
}
