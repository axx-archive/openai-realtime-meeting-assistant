/**
 * Tab ids map to live phone tool titles where we have a native surface:
 * BonfireOS (office), Rooms, Chat (scout threads), Board.
 */
export type MainTabParamList = {
  Home: undefined;
  Rooms: undefined;
  Chat: undefined;
  Board: undefined;
  More: undefined;
};

export type RootStackParamList = {
  Main: { screen?: keyof MainTabParamList } | undefined;
  Login: undefined;
  OSWeb: { path?: string; title?: string } | undefined;
  Room: { roomId: string; title: string };
  CreateRoom: undefined;
  Thread: { threadId: string; title: string };
  Intelligence: undefined;
  Memory: undefined;
  Meetings: undefined;
  Files: undefined;
  Alerts: undefined;
  Settings: undefined;
};
