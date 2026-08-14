import React from 'react';
import { api } from '../api/client';
import { CollectionScreen } from './CollectionScreen';

const memoryKeys = ['entries', 'memory', 'items'];
const intelligenceKeys = ['themes', 'signals', 'opportunities', 'priorities', 'items'];

export function MemoryScreen() {
  return (
    <CollectionScreen
      title="Memory"
      subtitle="The shared record across meetings, Scout, and the web"
      empty="Memory is quiet right now."
      keys={memoryKeys}
      load={api.memory}
      events={['memory']}
    />
  );
}

export { MeetingsScreen } from './MeetingsScreen';

export { FilesScreen } from './FilesScreen';

export function IntelligenceScreen() {
  return (
    <CollectionScreen
      title="Intelligence"
      subtitle="Themes and signals distilled from your company memory"
      empty="No current themes have been published."
      keys={intelligenceKeys}
      load={api.mission}
      events={['mission_insight', 'memory']}
      actionLabel="Refresh intelligence"
      actionHint="Runs a fresh native themes pass from shared company memory."
      action={api.refreshMission}
    />
  );
}
