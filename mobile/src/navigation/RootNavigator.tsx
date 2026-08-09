import React, { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Animated, StyleSheet, View, useColorScheme } from 'react-native';
import {
  NavigationContainer,
  DefaultTheme,
  createNavigationContainerRef,
} from '@react-navigation/native';
import { createNativeStackNavigator } from '@react-navigation/native-stack';
import { useAuth } from '../auth/AuthContext';
import { usePushRegistration } from '../push/usePushRegistration';
import type { PushTarget } from '../push/deepLink';
import { BoardScreen } from '../screens/BoardScreen';
import { CanvasScreen } from '../screens/CanvasScreen';
import { DeckScreen } from '../screens/DeckScreen';
import { LoginScreen } from '../screens/LoginScreen';
import { OSWebScreen } from '../screens/OSWebScreen';
import { RoomScreen } from '../screens/RoomScreen';
import { CreateRoomScreen } from '../screens/CreateRoomScreen';
import { ThreadScreen } from '../screens/ThreadScreen';
import { AlertsScreen } from '../screens/AlertsScreen';
import { AgentTeamScreen } from '../screens/AgentTeamScreen';
import {
  FilesScreen,
  IntelligenceScreen,
  MeetingsScreen,
  MemoryScreen,
} from '../screens/LibraryScreens';
import { SettingsScreen } from '../screens/SettingsScreen';
import {
  ContactInboxScreen,
  ContributionApprovalsScreen,
  CoworkerProfileScreen,
  NetworkBlocksScreen,
  NetworkDraftScreen,
  NetworkPreviewScreen,
  NetworkRecruiterViewScreen,
  NetworkSearchScreen,
  OrganizationPeopleScreen,
  OrganizationRecruitingScreen,
  OrganizationRequestsScreen,
  OrganizationsScreen,
  ProfileScreen,
  WorkRecordScreen,
} from '../screens/StrideProductScreens';
import { LaunchCradle } from '../components/CanvasCradleComposition';
import { duration, ease, useReduceMotion } from '../theme/motion';
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

type PendingPushTarget = {
  target: PushTarget;
  accountKey: string;
};

function pushAccountKey(email: string | undefined, sessionToken: string | null): string | null {
  const normalized = email?.trim().toLowerCase();
  return normalized && sessionToken ? normalized : null;
}

export function RootNavigator() {
  const { user, bootstrapping, sessionToken } = useAuth();
  const accountKey = pushAccountKey(user?.email, sessionToken);
  const dark = useColorScheme() === 'dark';
  const reduceMotion = useReduceMotion();
  const launchOpacity = useRef(new Animated.Value(1)).current;
  const [launchVisible, setLaunchVisible] = useState(true);
  const pendingPushTargetRef = useRef<PendingPushTarget | null>(null);
  const [pendingPushVersion, setPendingPushVersion] = useState(0);
  const committedPushAccountRef = useRef<string | null>(accountKey);

  useLayoutEffect(() => {
    if (committedPushAccountRef.current === accountKey) return;
    committedPushAccountRef.current = accountKey;
    if (pendingPushTargetRef.current?.accountKey !== accountKey) {
      pendingPushTargetRef.current = null;
    }
  }, [accountKey]);

  useEffect(() => {
    if (bootstrapping) {
      launchOpacity.setValue(1);
      setLaunchVisible(true);
      return;
    }
    if (reduceMotion) {
      launchOpacity.setValue(0);
      setLaunchVisible(false);
      return;
    }
    const fade = Animated.timing(launchOpacity, {
      toValue: 0,
      duration: duration.slow,
      easing: ease,
      useNativeDriver: true,
    });
    fade.start(({ finished }) => {
      if (finished) setLaunchVisible(false);
    });
    return () => fade.stop();
  }, [bootstrapping, launchOpacity, reduceMotion]);

  const openPushTarget = useCallback((target: PushTarget) => {
    // usePushRegistration derives this target from the current account's
    // authenticated notification projection. Recheck that binding at the
    // navigation boundary so an in-flight validation cannot cross a switch.
    if (!accountKey || target.accountKey !== accountKey) {
      pendingPushTargetRef.current = null;
      return;
    }
    if (!navigationRef.isReady()) {
      pendingPushTargetRef.current = {
        target,
        accountKey,
      };
      setPendingPushVersion((version) => version + 1);
      return;
    }
    navigationRef.navigate('Thread', {
      threadId: target.threadId,
      title: target.threadName ? `#${target.threadName.replace(/^#/, '')}` : '#team',
      messageId: target.messageId ?? undefined,
    });
    pendingPushTargetRef.current = null;
  }, [accountKey]);

  const flushPendingPushTarget = useCallback(() => {
    const pending = pendingPushTargetRef.current;
    if (!pending || bootstrapping) return;
    if (
      !accountKey
      || pending.accountKey !== accountKey
      || pending.target.accountKey !== accountKey
    ) {
      pendingPushTargetRef.current = null;
      return;
    }
    if (!navigationRef.isReady()) return;
    openPushTarget(pending.target);
  }, [accountKey, bootstrapping, openPushTarget]);

  useEffect(() => {
    flushPendingPushTarget();
  }, [flushPendingPushTarget, pendingPushVersion]);

  // A notification is a request to see ONE thing, so it opens the THREAD, never
  // the canvas — landing home would make the user navigate twice (§8).
  usePushRegistration({
    sessionToken,
    accountKey,
    bootstrapping,
    onOpenTarget: openPushTarget,
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
    return <LaunchCradle />;
  }

  return (
    <View style={styles.root}>
      <NavigationContainer ref={navigationRef} theme={navTheme} onReady={flushPendingPushTarget}>
        <Stack.Navigator screenOptions={{ headerShown: false }}>
        {user && sessionToken ? (
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
            <Stack.Screen
              name="Thread"
              component={ThreadScreen}
              options={{
                // Threads are working surfaces, never another sheet stacked on
                // top of the Deck sheet. Full-screen presentation removes the
                // rounded-sheet cap and dimmed strip visible when entering a
                // thread from the Threads segment.
                presentation: 'fullScreenModal',
                animation: 'simple_push',
              }}
            />
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
            <Stack.Screen name="AgentTeam" component={AgentTeamScreen} />
            <Stack.Screen name="Alerts" component={AlertsScreen} />
            <Stack.Screen name="Settings" component={SettingsScreen} />
            <Stack.Screen name="Profile" component={ProfileScreen} />
            <Stack.Screen name="WorkRecord" component={WorkRecordScreen} />
            <Stack.Screen name="Organizations" component={OrganizationsScreen} />
            <Stack.Screen name="OrganizationPeople" component={OrganizationPeopleScreen} />
            <Stack.Screen name="CoworkerProfile" component={CoworkerProfileScreen} />
            <Stack.Screen name="OrganizationRequests" component={OrganizationRequestsScreen} />
            <Stack.Screen name="OrganizationRecruiting" component={OrganizationRecruitingScreen} />
            <Stack.Screen name="ContributionApprovals" component={ContributionApprovalsScreen} />
            <Stack.Screen name="NetworkDraft" component={NetworkDraftScreen} />
            <Stack.Screen name="NetworkPreview" component={NetworkPreviewScreen} />
            <Stack.Screen name="NetworkRecruiterView" component={NetworkRecruiterViewScreen} />
            <Stack.Screen name="NetworkSearch" component={NetworkSearchScreen} />
            <Stack.Screen name="ContactInbox" component={ContactInboxScreen} />
            <Stack.Screen name="NetworkBlocks" component={NetworkBlocksScreen} />
          </>
        ) : (
          <Stack.Screen name="Login" component={LoginScreen} />
        )}
        </Stack.Navigator>
      </NavigationContainer>
      {launchVisible ? (
        <Animated.View
          pointerEvents="none"
          style={[styles.launchOverlay, { opacity: launchOpacity }]}
        >
          <LaunchCradle />
        </Animated.View>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
  },
  launchOverlay: {
    position: 'absolute',
    top: 0,
    right: 0,
    bottom: 0,
    left: 0,
    backgroundColor: colors.bgApp,
    zIndex: 20,
  },
});
