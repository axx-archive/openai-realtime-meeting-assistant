import React from 'react';
import { api } from '../api/client';
import { CollectionScreen } from './CollectionScreen';

const intelligenceKeys = ['themes', 'signals', 'opportunities', 'priorities', 'items'];

export { MemoryInspectorScreen as MemoryScreen } from './MemoryInspectorScreen';

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
