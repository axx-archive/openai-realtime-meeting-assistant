/**
 * Voice-first shell (design §4). There is no tab navigator: the Canvas is the
 * root, and everything else is pulled over it as the Deck — a native form sheet
 * whose three segments (Threads / Rooms / Work) answer three different questions
 * about the same company.
 */
export type DeckSegment = 'threads' | 'rooms' | 'work';

export type RootStackParamList = {
  Canvas: undefined;
  Deck: { segment?: DeckSegment } | undefined;
  Login: undefined;
  OSWeb: { path?: string; title?: string } | undefined;
  Room: { roomId: string; title: string };
  CreateRoom: undefined;
  Thread: { threadId: string; title: string };
  Intelligence: undefined;
  Memory: undefined;
  Meetings: undefined;
  Files: undefined;
  Board: undefined;
  Alerts: undefined;
  Settings: undefined;
};
