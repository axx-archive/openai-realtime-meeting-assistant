import React, { useCallback, useEffect, useRef } from 'react';
import {
  Animated,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { canvasCradleComposition } from '../components/CanvasCradleComposition';
import { StrideCradle } from '../components/StrideCradle';
import { useLiveLine } from '../canvas/useLiveLine';
import { liveLineDisplay } from '../canvas/liveLineDisplay';
import { usePersonalRealtime } from '../realtime/usePersonalRealtime';
import { duration, ease, useReduceMotion } from '../theme/motion';
import type { RootStackParamList } from '../navigation/types';
import { colors, radius, space, type } from '../theme/tokens';

type CanvasNav = NativeStackNavigationProp<RootStackParamList>;

/**
 * The Canvas — design §4 and §9.
 *
 * The root of the app is a conversation, not a dashboard. Realtime Scout stays
 * the only primary gesture. Typed work belongs in Work, where a user can start
 * a private chat or channel without competing with the primary navigation.
 *
 * Nothing here blocks first paint: the waveform renders before any network call
 * resolves, because time-to-first-word is the only performance metric that
 * matters (§C3).
 */

/**
 * The greeting carries no name — matching the web canon, whose
 * `officeLaunchGreeting` is simply "good morning."
 *
 * A name in a 36pt headline cannot be made robust. "Good afternoon,
 * Christopher." wraps; a display name stored as "Dr. May" greets you by your
 * title; initials, single names, and non-Latin scripts each break differently.
 * Every fix is another heuristic that is wrong for somebody, and a headline
 * that is occasionally wrong is worse than one that is never personal.
 *
 * The personal content belongs in the live line, which says something that
 * actually matters — who mentioned you, what is live — rather than proving the
 * app can read your account record.
 */
function greeting(): string {
  const hour = new Date().getHours();
  const part = hour < 12 ? 'morning' : hour < 18 ? 'afternoon' : 'evening';
  return `Good ${part}.`;
}

export function CanvasScreen() {
  const navigation = useNavigation<CanvasNav>();
  // No compact lockup competes with the live cradle on the Canvas.
  const reduceMotion = useReduceMotion();
  const live = useLiveLine();
  const lineDisplay = liveLineDisplay(live);
  const handleRealtimeActions = useCallback((actions: Array<Record<string, unknown>>) => {
    for (const action of actions) {
      const actionType = String(action.type ?? '').trim();
      const tool = String(action.tool ?? action.mode ?? '').trim();
      if (!['open_tool', 'assistant_mode'].includes(actionType)) continue;
      if (tool === 'chat') navigation.navigate('Deck', { segment: 'threads' });
      else if (['workflow', 'research', 'design', 'grill'].includes(tool)) {
        navigation.navigate('Deck', { segment: 'work' });
      } else if (tool === 'board') navigation.navigate('Board');
      else if (tool === 'artifacts' || tool === 'files') navigation.navigate('Files');
      else if (tool === 'meetings') navigation.navigate('Meetings');
      else if (tool === 'memory') navigation.navigate('Memory');
      else if (tool === 'intelligence') navigation.navigate('Intelligence');
      else if (tool === 'notifications' || tool === 'alerts') navigation.navigate('Alerts');
      else if (tool === 'settings') navigation.navigate('Settings');
    }
  }, [navigation]);
  const realtime = usePersonalRealtime({ onActions: handleRealtimeActions });
  const listening = realtime.active;

  const handleTap = useCallback(async () => {
    // The cradle has one stable meaning: a full-duplex Realtime Scout call.
    // Typed and dictated messages belong to conversation screens in Work.
    if (!realtime.enabled) return;
    if (realtime.active) {
      await realtime.stop('completed');
    } else {
      if (realtime.status === 'error') await realtime.stop('cancelled');
      await realtime.start();
    }
  }, [realtime]);

  const voiceError = realtime.enabled
    ? realtime.error
    : 'Live Scout is unavailable in this build.';

  // The live line routes to whatever it is actually talking about. A line that
  // names a specific message and then dumps you in a thread list would make the
  // user navigate twice to reach the thing they were just shown.
  const openLiveTarget = useCallback(() => {
    if (live.threadId) {
      navigation.navigate('Thread', {
        threadId: live.threadId,
        title: live.threadTitle ?? '#team',
        messageId: live.messageId ?? undefined,
      });
      return;
    }
    if (live.kind === 'rooms') {
      navigation.navigate('Deck', { segment: 'rooms' });
      return;
    }
    navigation.navigate('Deck', { segment: 'threads' });
  }, [live.kind, live.threadId, navigation]);

  // Cross-fade plus a 4pt rise when the line's content changes. transform and
  // opacity only (motion canon §8.4); Reduce Motion sets the values outright so
  // the CONTENT still updates and only the movement goes away.
  const liveFade = useRef(new Animated.Value(1)).current;
  const liveRise = useRef(new Animated.Value(0)).current;
  useEffect(() => {
    if (!live.text) return;
    if (reduceMotion) {
      liveFade.setValue(1);
      liveRise.setValue(0);
      return;
    }
    liveFade.setValue(0);
    liveRise.setValue(4);
    Animated.parallel([
      Animated.timing(liveFade, {
        toValue: 1,
        duration: duration.med,
        easing: ease,
        useNativeDriver: true,
      }),
      Animated.timing(liveRise, {
        toValue: 0,
        duration: duration.med,
        easing: ease,
        useNativeDriver: true,
      }),
    ]).start();
  }, [ease, liveFade, liveRise, live.author, live.text, reduceMotion]);

  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right', 'bottom']}>
      <ScrollView
        contentContainerStyle={canvasCradleComposition.body}
        keyboardShouldPersistTaps="handled"
        showsVerticalScrollIndicator={false}
      >
          <View style={canvasCradleComposition.skyAbove} />

          {/* One voice control. Static identity belongs to launch/login chrome. */}
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={realtime.status === 'connecting' ? 'Connecting to Scout' : listening ? 'Listening' : 'Talk to Scout'}
            accessibilityHint={listening ? 'Tap to end this voice conversation.' : 'Tap to start a voice conversation.'}
            onPress={() => { void handleTap(); }}
            style={({ pressed }) => [canvasCradleComposition.wave, pressed && styles.wavePressed]}
          >
            <StrideCradle
              trace={realtime.trace}
              listening={listening}
              source={realtime.status === 'talking' ? 'agent' : 'human'}
            />
          </Pressable>

          <View style={canvasCradleComposition.copyBlock}>
            <Text style={styles.greeting}>{greeting()}</Text>

            {/* Every decision about WHAT text appears lives in liveLineDisplay,
                so it can be asserted without a React renderer — this component
                keeps only the styling and the motion. */}
            {lineDisplay.visible ? (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel={lineDisplay.accessibilityLabel}
                accessibilityHint={lineDisplay.accessibilityHint}
                onPress={openLiveTarget}
                style={({ pressed }) => [styles.liveLine, pressed && styles.pressed]}
              >
                <Animated.Text
                  style={[
                    styles.liveText,
                    live.mentioned && styles.liveMention,
                    { opacity: liveFade, transform: [{ translateY: liveRise }] },
                  ]}
                  // Two lines, then ellipsis. A long message must never push the
                  // bottom utility row around — the composition is load-bearing.
                  numberOfLines={2}
                  ellipsizeMode="tail"
                >
                  {lineDisplay.authorSpan ? (
                    <Text style={styles.liveAuthor}>{lineDisplay.authorSpan}</Text>
                  ) : null}
                  {lineDisplay.bodySpan}
                </Animated.Text>
              </Pressable>
            ) : null}
          </View>

          {voiceError ? (
            <Text style={styles.error}>{voiceError}</Text>
          ) : null}

        <View style={canvasCradleComposition.skyBelow} />
      </ScrollView>

    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  /**
   * Composition, not centring. Flex spacers weighted 1.25 : 1 sit the content
   * group slightly BELOW true centre.
   *
   * The rule: empty space above content reads as sky and is felt as
   * intentional; the same emptiness below content, stacked above a heavy UI
   * element, reads as a gap where something is missing. Geometric centring gave
   * 185pt above and 232pt below — the wrong way round, and the single biggest
   * reason the screen felt unfinished.
   */
  wavePressed: {
    transform: [{ scale: 0.96 }],
    opacity: 0.88,
  },
  greeting: {
    // The only sentence on the page, so it carries the page rather than sharing
    // weight with the live line. Tracking tightens as size grows — at 36pt the
    // default spacing reads loose and juvenile.
    fontSize: 36,
    fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600',
    letterSpacing: -1,
    lineHeight: 42,
    color: colors.text1,
    textAlign: 'center',
    // Bound to the greeting so the two read as one unit, not two stacked items.
    marginBottom: space[2],
  },
  liveLine: {
    paddingHorizontal: space[3],
    paddingVertical: 6,
    borderRadius: radius.md,
    // Wraps to two lines at most on a long sentence, and keeps the measure
    // short enough to stay readable centred.
    maxWidth: 330,
  },
  pressed: { opacity: 0.6 },
  liveText: {
    fontSize: 15,
    fontFamily: 'GoogleSansFlex_400Regular', fontWeight: '400',
    letterSpacing: -0.08,
    lineHeight: 21,
    color: colors.text2,
    textAlign: 'center',
  },
  liveMention: {
    color: colors.text1,
  },
  liveAuthor: {
    // The author carries the line's weight; the message stays in text2 so the
    // pair reads as one sentence rather than two competing items.
    color: colors.text1,
    fontFamily: 'GoogleSansFlex_500Medium', fontWeight: '500',
  },
  error: {
    ...type.bodySm,
    color: colors.danger,
    textAlign: 'center',
  },
});
