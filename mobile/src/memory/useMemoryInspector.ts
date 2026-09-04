import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../api/client';
import type { MemoryInspectItem } from '../api/types';

export function useMemoryInspector(session: string | null, active: boolean, subject: string, kind: string, person: string, eventVersion: number) {
  const [snapshot, setSnapshot] = useState<{ key: string; items: MemoryInspectItem[] } | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const key = session && active ? JSON.stringify([session, subject, kind, person]) : '';
  const currentKey = useRef(key);
  currentKey.current = key;
  const generation = useRef(0);
  const refresh = useCallback(async () => {
    const version = ++generation.current;
    if (!session || !key) { setSnapshot(null); setLoading(false); setError(''); return; }
    setLoading(true);
    try {
      const response = await api.memoryInspect(session, { subject, kinds: kind, person });
      if (version !== generation.current || currentKey.current !== key) return;
      if (!response.ok || !Array.isArray(response.items)) throw new Error('Invalid memory response');
      setSnapshot({ key, items: response.items });
      setError('');
    } catch {
      if (version !== generation.current || currentKey.current !== key) return;
      setSnapshot(null);
      setError('Memory could not refresh. Try again to check current records and access.');
    } finally {
      if (version === generation.current && currentKey.current === key) setLoading(false);
    }
  }, [session, key, subject, kind, person]);
  useEffect(() => {
    void refresh();
    return () => { generation.current += 1; };
  }, [refresh, eventVersion]);
  return { items: key && snapshot?.key === key ? snapshot.items : [], loading, error, refresh };
}
