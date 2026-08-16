/**
 * Voice-first shell (design §4). The Canvas is the root. Meet and Chat are
 * proper card destinations that fill the content area (with the rail visible
 * on phone, sidebar on tablet ≥744). The Deck remains available for Work
 * segments but Meet and Chat are no longer formSheet overlays over Home.
 */
export type DeckSegment = 'threads' | 'rooms' | 'work';

export type RootStackParamList = {
  Canvas: undefined;
  Meet: undefined;
  Chat: undefined;
  WorkHome: undefined;
  NetworkHome: undefined;
  WorkSearchHome: undefined;
  YouHome: undefined;
  Deck: { segment?: DeckSegment } | undefined;
  Login: undefined;
  OSWeb: { path?: string; title?: string } | undefined;
  Room: { roomId: string; title: string };
  CreateRoom: undefined;
  NewConversation: undefined;
  Thread: {
    threadId: string;
    title: string;
    messageId?: string;
    riffSpace?: boolean;
    sourceThreadId?: string;
    sourceTitle?: string;
    displayMode?: 'screen' | 'sheet' | 'rail';
  };
  ChannelRiff: {
    threadId: string;
    title: string;
    messageId?: string;
    riffSpace: true;
    sourceThreadId: string;
    sourceTitle: string;
    displayMode: 'screen' | 'sheet' | 'rail';
  };
  Intelligence: undefined;
  Memory: undefined;
  Meetings: { meetingId?: string; segmentId?: string; returnToRoomId?: string; returnMode?: 'recap' | 'transcript' } | undefined;
  Files: { fileId?: string } | undefined;
  AgentTeam: undefined;
  Board: { cardId?: string } | undefined;
  Alerts: undefined;
  Settings: undefined;
  Profile: undefined;
  WorkRecord: undefined;
  Organizations: undefined;
  OrganizationPeople: undefined;
  CoworkerProfile: { person: string };
  OrganizationRequests: undefined;
  OrganizationRecruiting: undefined;
  ContributionApprovals: undefined;
  NetworkDraft: undefined;
  NetworkPreview: undefined;
  NetworkRecruiterView: undefined;
  NetworkSearch: undefined;
  ContactInbox: undefined;
  NetworkBlocks: undefined;
};
