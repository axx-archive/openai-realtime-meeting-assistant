import React, { createContext, useContext } from 'react';
import type { usePersonalRealtime } from './usePersonalRealtime';

export type PersonalRealtimeController = ReturnType<typeof usePersonalRealtime>;

const PersonalRealtimeContext = createContext<PersonalRealtimeController | null>(null);

export function PersonalRealtimeContextProvider({
  children,
  value,
}: {
  children: React.ReactNode;
  value: PersonalRealtimeController;
}) {
  return (
    <PersonalRealtimeContext.Provider value={value}>
      {children}
    </PersonalRealtimeContext.Provider>
  );
}

export function usePersonalRealtimeContext(): PersonalRealtimeController {
  const realtime = useContext(PersonalRealtimeContext);
  if (!realtime) {
    throw new Error('usePersonalRealtimeContext must be used inside PersonalRealtimeProvider');
  }
  return realtime;
}

export function useOptionalPersonalRealtimeContext(): PersonalRealtimeController | null {
  return useContext(PersonalRealtimeContext);
}
