import React, { useEffect } from 'react';
import { PersonalRealtimeContextProvider } from './PersonalRealtimeContext';
import { usePersonalRealtime } from './usePersonalRealtime';

export function PersonalRealtimeProvider({
  children,
  onActions,
  roomActive = false,
}: {
  children: React.ReactNode;
  onActions?: (actions: Array<Record<string, unknown>>) => void;
  roomActive?: boolean;
}) {
  const realtime = usePersonalRealtime({ onActions });
  useEffect(() => {
    if (roomActive && realtime.active) void realtime.stop('cancelled');
  }, [realtime.active, realtime.stop, roomActive]);
  return (
    <PersonalRealtimeContextProvider value={realtime}>
      {children}
    </PersonalRealtimeContextProvider>
  );
}
