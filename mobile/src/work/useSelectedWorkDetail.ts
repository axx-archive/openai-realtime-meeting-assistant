import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../api/client';
import type { StudioProject } from '../api/types';

/** Detail is fetched only for the open selection, never every list row. */
export function useSelectedWorkDetail(session: string | null, id: string, active: boolean, refreshVersion: number) {
  const [snapshot, setSnapshot] = useState<{ key: string; project: StudioProject } | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const generation = useRef(0);
  const key = session && id && active ? `${session}:${id}` : '';
  const currentKey = useRef(key);
  currentKey.current = key;
  const refresh = useCallback(async () => {
    const request = ++generation.current;
    if (!key || !session) { setSnapshot(null); setError(''); setLoading(false); return; }
    setLoading(true);
    try {
      const response = await api.studioProject(session, id);
      if (generation.current !== request || currentKey.current !== key) return;
      if (!response.ok || response.project.id !== id) throw new Error('Invalid Work detail response');
      setSnapshot({ key, project: response.project });
      setError('');
    } catch {
      if (generation.current !== request || currentKey.current !== key) return;
      setSnapshot(null);
      setError('This work could not refresh. Try again to check its current version and access.');
    } finally {
      if (generation.current === request && currentKey.current === key) setLoading(false);
    }
  }, [id, key, session]);
  useEffect(() => {
    void refresh();
    return () => { generation.current += 1; };
  }, [refresh, refreshVersion]);
  const replace = useCallback((project: StudioProject) => {
    if (currentKey.current === key && key && project.id === id) {
      generation.current += 1;
      setSnapshot({ key, project });
      setLoading(false);
      setError('');
    }
  }, [id, key]);
  const clear = useCallback(() => { generation.current += 1; setSnapshot(null); }, []);
  return { project: key && snapshot?.key === key ? snapshot.project : null, loading, error, refresh, replace, clear };
}
