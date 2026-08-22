import React, { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { AccessibilityInfo, Platform, StyleSheet, View, useColorScheme, useWindowDimensions } from 'react-native';
import {
  NavigationContainer,
  DefaultTheme,
  createNavigationContainerRef,
} from '@react-navigation/native';
import { createNativeStackNavigator } from '@react-navigation/native-stack';
import { useAuth } from '../auth/AuthContext';
import { usePushRegistration } from '../push/usePushRegistration';
import type { PushTarget } from '../push/deepLink';
import { CanvasScreen } from '../screens/CanvasScreen';
import { ChatScreen } from '../screens/ChatScreen';
import { DeckScreen } from '../screens/DeckScreen';
import { LoginScreen } from '../screens/LoginScreen';
import { MeetScreen } from '../screens/MeetScreen';
import { OSWebScreen } from '../screens/OSWebScreen';
import { DeckViewerScreen } from '../screens/DeckViewerScreen';
import { RoomScreen } from '../screens/RoomScreen';
import { CreateRoomScreen } from '../screens/CreateRoomScreen';
import { ChannelRiffScreen, ThreadScreen } from '../screens/ThreadScreen';
import { NewConversationScreen } from '../screens/NewConversationScreen';
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
  ContributionApprovalsScreen,
  CoworkerProfileScreen,
  NetworkDraftScreen,
  OrganizationPeopleScreen,
  OrganizationRecruitingScreen,
  OrganizationRequestsScreen,
  OrganizationsScreen,
  ProfileScreen,
  WorkRecordScreen,
} from '../screens/StrideProductScreens';
import { LaunchCradle } from '../components/CanvasCradleComposition';
import type { RootStackParamList } from './types';
import { NativeUniversalShell } from './NativeUniversalShell';
import { PersonalRealtimeProvider } from '../realtime/PersonalRealtimeProvider';
import { personalRealtimeIslandSurface } from '../realtime/personalRealtimeIslandPlacement';
import {
  nativeShellDestinationForRoute,
  createNativeShellSelectionCoordinator,
  nativeShellDestinations,
  nativeShellDestinationAllowed,
  nativeShellVisibleForRoute,
} from './nativeShellModel';
import {
  NetworkHomeScreen,
  WorkHomeScreen,
  WorkSearchHomeScreen,
  YouHomeScreen,
} from '../screens/NativeShellScreens';

const Stack = createNativeStackNavigator<RootStackParamList>();

/**
 * The voice-first shell — design §4.
 *
 * The Canvas remains the voice-first Home root. PD1 adds one universal,
 * semantic destination shell around this stack: a compact iPhone rail and an
 * adaptive iPad sidebar.
 *
 * Meet and Chat are now proper card destinations that fill the content area
 * (with the rail visible on phone, sidebar on tablet ≥744). They are no longer
 * peek sheets over Home. The Deck remains available for backward compatibility
 * and the Work segment.
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

/** iPad workstation threshold — rail stays visible for New/CreateRoom at ≥1024. */
const WORKSTATION_MIN_WIDTH = 1024;

export function RootNavigator() {
  const { user, bootstrapping, sessionToken } = useAuth();
  const accountKey = pushAccountKey(user?.email, sessionToken);
  const dark = useColorScheme() === 'dark';
  const { width } = useWindowDimensions();
  const isIPad = Platform.OS === 'ios' && Platform.isPad;
  const isWorkstationWidth = isIPad && width >= WORKSTATION_MIN_WIDTH;
  const pendingPushTargetRef = useRef<PendingPushTarget | null>(null);
  const [pendingPushVersion, setPendingPushVersion] = useState(0);
  const [activeRoute, setActiveRoute] = useState<keyof RootStackParamList | undefined>('Canvas');
  const [activeShellDestination, setActiveShellDestination] = useState(nativeShellDestinationForRoute('Canvas'));
  const shellSelectionRef = useRef(createNativeShellSelectionCoordinator(
    (message) => AccessibilityInfo.announceForAccessibility(message),
  ));
  const committedPushAccountRef = useRef<string | null>(accountKey);

  useLayoutEffect(() => {
    if (committedPushAccountRef.current === accountKey) return;
    committedPushAccountRef.current = accountKey;
    if (pendingPushTargetRef.current?.accountKey !== accountKey) {
      pendingPushTargetRef.current = null;
    }
  }, [accountKey]);

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

  const selectShellDestination = useCallback((destination: (typeof nativeShellDestinations)[number]) => {
    if (!navigationRef.isReady() || !nativeShellDestinationAllowed(destination.id, user?.shellAccess)) return;
    shellSelectionRef.current.select(destination, (route, params) => navigationRef.navigate({ name: route, params } as never));
  }, [user?.shellAccess]);

  useEffect(() => {
    if (!user || user.shellAccess === 'full' || !navigationRef.isReady()) return;
    if (!nativeShellDestinationAllowed(activeShellDestination, user.shellAccess)) {
      navigationRef.navigate('Canvas');
    }
  }, [activeShellDestination, user]);

  const syncActiveRoute = useCallback(() => {
    const currentRoute = navigationRef.getCurrentRoute();
    const route = currentRoute?.name as keyof RootStackParamList | undefined;
    setActiveShellDestination(shellSelectionRef.current.commit(route, currentRoute?.params));
    setActiveRoute(route);
  }, []);

  const handleRealtimeActions = useCallback((actions: Array<Record<string, unknown>>) => {
    if (!navigationRef.isReady()) return;
    for (const action of actions) {
      const actionType = String(action.type ?? '').trim();
      if (actionType === 'open_chat_thread') {
        const threadId = String(action.threadId ?? '').trim();
        const title = String(action.title ?? '').trim();
        const visibility = String(action.visibility ?? '').trim();
        if (threadId) {
          navigationRef.navigate('Thread', {
            threadId,
            title: visibility === 'public' ? `#${title.replace(/^#/, '')}` : title || 'Private chat',
          });
        }
        continue;
      }
      const tool = String(action.tool ?? action.mode ?? '').trim();
      if (!['open_tool', 'assistant_mode'].includes(actionType)) continue;
      if (tool === 'chat') navigationRef.navigate('Chat');
      else if (['workflow', 'research', 'design', 'grill'].includes(tool)) {
        navigationRef.navigate('WorkHome');
      } else if (tool === 'board') navigationRef.navigate('WorkHome');
      else if (tool === 'artifacts' || tool === 'files') navigationRef.navigate('Files');
      else if (tool === 'meetings') navigationRef.navigate('Meetings');
      else if (tool === 'memory') navigationRef.navigate('Memory');
      else if (tool === 'intelligence') navigationRef.navigate('Intelligence');
      else if (tool === 'notifications' || tool === 'alerts') navigationRef.navigate('Alerts');
      else if (tool === 'settings') navigationRef.navigate('Settings');
    }
  }, []);

  const openPersonalRealtimeThread = useCallback((threadId: string) => {
    if (!navigationRef.isReady()) return;
    navigationRef.navigate('Thread', { threadId, title: 'Scout voice' });
  }, []);

  if (bootstrapping) {
    return <LaunchCradle />;
  }

  return (
    <View style={styles.root}>
      <PersonalRealtimeProvider onActions={handleRealtimeActions} roomActive={activeRoute === 'Room'}>
        <NativeUniversalShell
          active={activeShellDestination}
          access={user?.shellAccess}
          keepSidebarForFocusedRoute={Boolean(user && sessionToken && (
            activeRoute === 'Thread'
            || activeRoute === 'ChannelRiff'
            || (isWorkstationWidth && (activeRoute === 'NewConversation' || activeRoute === 'CreateRoom'))
          ))}
          personalRealtimeSurface={personalRealtimeIslandSurface(activeRoute)}
          personalRealtimeStartAllowed={Boolean(user && sessionToken && activeRoute !== 'Room')}
          personalRealtimeVisible={Boolean(
            user
            && sessionToken
            && activeRoute !== 'Room'
            && nativeShellVisibleForRoute(activeRoute)
          )}
          visible={Boolean(user && sessionToken && nativeShellVisibleForRoute(activeRoute))}
          onOpenPersonalRealtimeThread={openPersonalRealtimeThread}
          onSelect={selectShellDestination}
        >
        <NavigationContainer
          ref={navigationRef}
          theme={navTheme}
          onReady={() => { syncActiveRoute(); flushPendingPushTarget(); }}
          onStateChange={syncActiveRoute}
        >
        <Stack.Navigator screenOptions={{ headerShown: false }}>
        {user && sessionToken ? (
          <>
            <Stack.Screen name="Canvas" component={CanvasScreen} />
            <Stack.Screen name="Meet" component={MeetScreen} />
            <Stack.Screen name="Chat" component={ChatScreen} />
            <Stack.Screen name="WorkHome" component={WorkHomeScreen} />
            <Stack.Screen name="NetworkHome" component={NetworkHomeScreen} />
            <Stack.Screen name="WorkSearchHome" component={WorkSearchHomeScreen} />
            <Stack.Screen name="YouHome" component={YouHomeScreen} />
            <Stack.Screen
              name="Deck"
              component={DeckScreen}
              options={{
                presentation: 'formSheet',
                sheetAllowedDetents: DECK_DETENTS,
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
                // A thread is a normal working destination, not another sheet.
                // Keeping it in the stack lets the iPad retain the universal
                // STRIDE sidebar plus its contextual conversation list. The
                // compact shell is still hidden on iPhone, so the phone remains
                // a focused full-screen conversation.
                presentation: 'card',
                animation: 'simple_push',
              }}
            />
            <Stack.Screen
              name="ChannelRiff"
              component={ChannelRiffScreen}
              options={({ route }) => route.params.displayMode === 'sheet'
                ? {
                    presentation: 'formSheet',
                    sheetAllowedDetents: [0.72, 1],
                    sheetInitialDetentIndex: 0,
                    sheetGrabberVisible: true,
                    sheetCornerRadius: 28,
                    animation: 'slide_from_bottom',
                  }
                : {
                    presentation: 'card',
                    animation: 'simple_push',
                  }}
            />
            {/* A call is not a sheet — joining a room takes the full screen. */}
            <Stack.Screen name="Room" component={RoomScreen} />
            <Stack.Screen
              name="CreateRoom"
              component={CreateRoomScreen}
              options={({ route }) => route.params?.displayMode === 'workstation'
                ? { presentation: 'card', animation: 'simple_push' }
                : { presentation: 'formSheet', sheetAllowedDetents: [0.72, 1], sheetInitialDetentIndex: 0, sheetGrabberVisible: true, sheetCornerRadius: 28 }}
            />
            <Stack.Screen
              name="NewConversation"
              component={NewConversationScreen}
              options={({ route }) => route.params?.displayMode === 'workstation'
                ? { presentation: 'card', animation: 'simple_push' }
                : { presentation: 'formSheet', sheetAllowedDetents: [0.72, 1], sheetInitialDetentIndex: 0, sheetGrabberVisible: true, sheetCornerRadius: 28 }}
            />
            <Stack.Screen
              name="OSWeb"
              component={OSWebScreen}
              options={{ presentation: 'modal', animation: 'slide_from_bottom' }}
            />
            <Stack.Screen
              name="DeckViewer"
              component={DeckViewerScreen}
              options={{
                presentation: 'fullScreenModal',
                animation: 'fade',
                gestureEnabled: true,
              }}
            />
            {/* Legacy Board deep links land in Work. The old filing-system UI
                is no longer mounted, while its persisted history remains. */}
            <Stack.Screen name="Board" component={WorkHomeScreen} />
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
            <Stack.Screen name="NetworkPreview" component={NetworkHomeScreen} />
            {/* Parent-off routes resolve to shell-owned opaque state. Their data
                components are deliberately not mounted until their server
                parents are qualified and the native carrier is revised. */}
            <Stack.Screen name="NetworkRecruiterView" component={NetworkHomeScreen} />
            <Stack.Screen name="NetworkSearch" component={WorkSearchHomeScreen} />
            <Stack.Screen name="ContactInbox" component={WorkSearchHomeScreen} />
            <Stack.Screen name="NetworkBlocks" component={NetworkHomeScreen} />
          </>
        ) : (
          <Stack.Screen name="Login" component={LoginScreen} />
        )}
        </Stack.Navigator>
        </NavigationContainer>
        </NativeUniversalShell>
      </PersonalRealtimeProvider>
    </View>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
  },
});
