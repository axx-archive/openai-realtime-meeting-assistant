import React from 'react';
import { ActivityIndicator, StyleSheet, View, useColorScheme } from 'react-native';
import {
  NavigationContainer,
  DefaultTheme,
  createNavigationContainerRef,
} from '@react-navigation/native';
import { createNativeStackNavigator } from '@react-navigation/native-stack';
import { useAuth } from '../auth/AuthContext';
import { usePushRegistration } from '../push/usePushRegistration';
import { BoardScreen } from '../screens/BoardScreen';
import { CanvasScreen } from '../screens/CanvasScreen';
import { DeckScreen } from '../screens/DeckScreen';
import { LoginScreen } from '../screens/LoginScreen';
import { OSWebScreen } from '../screens/OSWebScreen';
import { RoomScreen } from '../screens/RoomScreen';
import { CreateRoomScreen } from '../screens/CreateRoomScreen';
import { ThreadScreen } from '../screens/ThreadScreen';
import { AlertsScreen } from '../screens/AlertsScreen';
import {
  FilesScreen,
  IntelligenceScreen,
  MeetingsScreen,
  MemoryScreen,
} from '../screens/LibraryScreens';
import { SettingsScreen } from '../screens/SettingsScreen';
import { colors } from '../theme/tokens';
import type { RootStackParamList } from './types';

const Stack = createNativeStackNavigator<RootStackParamList>();

/**
 * The voice-first shell — design §4.
 *
 * There is no tab navigator. The Canvas is the root, and the Deck is a native
 * form sheet pulled over it. Everything the old tab bar held is now either a
 * Deck segment or a Deck destination.
 *
 * The detents are real `UISheetPresentationController` detents rather than a
 * hand-rolled pan sheet, which buys correct rubber-banding, interactive
 * dismissal, and VoiceOver behaviour for free:
 *
 *   0.14  peek  — the segment header and the first rows; a glance, not a commit
 *   0.5   half
 *   1.0   full
 */
const DECK_DETENTS = [0.14, 0.5, 1];

/**
 * A ref rather than the `useNavigation` hook, because push taps arrive from
 * outside the React tree — including on a cold start, before any screen has
 * mounted.
 */
export const navigationRef = createNavigationContainerRef<RootStackParamList>();

export function RootNavigator() {
  const { user, bootstrapping, sessionToken } = useAuth();
  const dark = useColorScheme() === 'dark';

  // A notification is a request to see ONE thing, so it opens the THREAD, never
  // the canvas — landing home would make the user navigate twice (§8).
  usePushRegistration({
    sessionToken,
    onOpenTarget: (target) => {
      if (!navigationRef.isReady()) return;
      navigationRef.navigate('Thread', {
        threadId: target.threadId,
        title: target.threadName ?? '#team',
      });
    },
  });
  const navTheme = {
    ...DefaultTheme,
    dark,
    colors: {
      ...DefaultTheme.colors,
      background: dark ? '#09090B' : '#F5F5F7',
      card: dark ? '#141418' : '#FFFFFF',
      text: dark ? '#F7F7F9' : '#0E0E10',
      border: dark ? 'rgba(255,255,255,0.09)' : 'rgba(14,14,16,0.08)',
      primary: dark ? '#F7F7F9' : '#0E0E10',
    },
  };

  if (bootstrapping) {
    return (
      <View style={styles.boot}>
        <ActivityIndicator size="large" color={colors.accent} />
      </View>
    );
  }

  return (
    <NavigationContainer ref={navigationRef} theme={navTheme}>
      <Stack.Navigator screenOptions={{ headerShown: false }}>
        {user ? (
          <>
            <Stack.Screen name="Canvas" component={CanvasScreen} />
            <Stack.Screen
              name="Deck"
              component={DeckScreen}
              options={{
                presentation: 'formSheet',
                sheetAllowedDetents: DECK_DETENTS,
                // Opens at half: enough to be useful on arrival without
                // burying the canvas the user just left.
                sheetInitialDetentIndex: 1,
                sheetGrabberVisible: true,
                sheetCornerRadius: 28,
                animation: 'slide_from_bottom',
              }}
            />
            <Stack.Screen name="Thread" component={ThreadScreen} />
            {/* A call is not a sheet — joining a room takes the full screen. */}
            <Stack.Screen name="Room" component={RoomScreen} />
            <Stack.Screen
              name="CreateRoom"
              component={CreateRoomScreen}
              options={{ presentation: 'formSheet' }}
            />
            <Stack.Screen
              name="OSWeb"
              component={OSWebScreen}
              options={{ presentation: 'modal', animation: 'slide_from_bottom' }}
            />
            <Stack.Screen name="Board" component={BoardScreen} />
            <Stack.Screen name="Intelligence" component={IntelligenceScreen} />
            <Stack.Screen name="Memory" component={MemoryScreen} />
            <Stack.Screen name="Meetings" component={MeetingsScreen} />
            <Stack.Screen name="Files" component={FilesScreen} />
            <Stack.Screen name="Alerts" component={AlertsScreen} />
            <Stack.Screen name="Settings" component={SettingsScreen} />
          </>
        ) : (
          <Stack.Screen name="Login" component={LoginScreen} />
        )}
      </Stack.Navigator>
    </NavigationContainer>
  );
}

const styles = StyleSheet.create({
  boot: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.bgApp,
  },
});
