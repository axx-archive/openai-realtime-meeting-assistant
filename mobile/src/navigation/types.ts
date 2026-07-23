export type MainTabParamList = {
  Home: undefined;
  Rooms: undefined;
  Scout: undefined;
  Board: undefined;
};

export type RootStackParamList = {
  Main: { screen?: keyof MainTabParamList } | undefined;
  Login: undefined;
  OSWeb: { path?: string; title?: string } | undefined;
};
