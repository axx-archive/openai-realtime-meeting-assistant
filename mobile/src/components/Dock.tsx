import React, { useCallback, useMemo, useRef } from 'react';
import {
  PanResponder,
  Pressable,
  StyleSheet,
  Text,
  View,
  type GestureResponderEvent,
} from 'react-native';
import { SymbolView } from 'expo-symbols';
import { Glass } from '../theme/glass';
import { Waveform } from './Waveform';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import type { DictationState } from '../voice/useDictation';

/**
 * The Dock — design §5.
 *
 * One surface, three affordances, distinguished by gesture rather than by three
 * competing targets in the same 60pt of thumb-reachable screen:
 *
 *   Tap      → converse. A loop: you speak, Scout answers, the mic re-arms.
 *   Hold     → dictate. One shot into a field. Slide away to cancel.
 *   Drag up  → reveal the Deck.
 *
 * The distinction that carries the design is LOOP versus ONE-SHOT, not duplex
 * versus half-duplex — which is why the mapping stays true when Wave 2 swaps the
 * conversation transport for a live WebRTC line (§17).
 */

/** Press longer than this is a hold, not a tap. */
const HOLD_MS = 220;
/** Upward travel that commits to revealing the Deck. */
const REVEAL_DY = -40;
/** Sliding this far from the Dock while holding cancels the dictation. */
const CANCEL_DY = -90;

export type DockProps = {
  dictation: DictationState;
  /** Rolling amplitude history; drives the pill's waveform while listening. */
  trace: readonly number[];
  /** True while the tap-to-converse loop is open. */
  conversing: boolean;
  onTap: () => void;
  onHoldStart: () => void;
  onHoldEnd: () => void;
  onHoldCancel: () => void;
  onReveal: () => void;
  /** Silent path (§9.5) — opens the keyboard composer instead of the mic. */
  onKeyboard: () => void;
};

export function Dock({
  dictation,
  trace,
  conversing,
  onTap,
  onHoldStart,
  onHoldEnd,
  onHoldCancel,
  onReveal,
  onKeyboard,
}: DockProps) {
  const holdTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const holding = useRef(false);
  const cancelled = useRef(false);

  const listening = dictation === 'listening';
  const busy = dictation === 'transcribing';
  const live = listening || conversing;

  const clearHoldTimer = () => {
    if (holdTimer.current) {
      clearTimeout(holdTimer.current);
      holdTimer.current = null;
    }
  };

  const handlePressIn = useCallback(() => {
    cancelled.current = false;
    holdTimer.current = setTimeout(() => {
      holding.current = true;
      onHoldStart();
    }, HOLD_MS);
  }, [onHoldStart]);

  const handlePressOut = useCallback(() => {
    clearHoldTimer();
    if (holding.current) {
      holding.current = false;
      // Cancellation was already signalled by the pan handler the moment the
      // finger crossed the threshold — re-signalling here would fire a second
      // haptic for one gesture. `onHoldEnd` still runs either way, because the
      // recorder has to be stopped whether or not the result is discarded.
      onHoldEnd();
      return;
    }
    // Released before the hold threshold — a tap.
    onTap();
  }, [onHoldCancel, onHoldEnd, onTap]);

  // Vertical drag. Only claims the gesture on real upward travel, so it never
  // steals a tap or a hold from the Pressable underneath.
  const pan = useMemo(
    () =>
      PanResponder.create({
        onMoveShouldSetPanResponder: (_event, gesture) => gesture.dy < -8,
        onPanResponderMove: (_event, gesture) => {
          // Slide-away-to-cancel: while dictating, upward travel aborts rather
          // than reveals. A misfired dictation that commits garbage text is the
          // single most stressful failure in a voice UI.
          if (holding.current && gesture.dy < CANCEL_DY && !cancelled.current) {
            cancelled.current = true;
            onHoldCancel();
          }
        },
        onPanResponderRelease: (_event, gesture) => {
          if (holding.current) return;
          if (gesture.dy < REVEAL_DY) onReveal();
        },
      }),
    [onHoldCancel, onReveal],
  );

  const label = listening
    ? 'Listening'
    : busy
      ? 'Transcribing'
      : conversing
        ? 'Talking to Scout'
        : 'Talk to Scout';

  return (
    <View style={styles.wrap} {...pan.panHandlers}>
      <Glass
        radius={radius.full}
        interactive
        variant="regular"
        tint={live ? colors.emberSoft : undefined}
        style={styles.pill}
      >
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={label}
          accessibilityHint="Tap to talk to Scout. Touch and hold to dictate. Swipe up to open threads, rooms, and work."
          // Screen readers get the STATE, not a stream of bar heights (§9.5).
          accessibilityValue={listening ? { text: label } : undefined}
          accessibilityState={{ busy }}
          onPressIn={handlePressIn}
          onPressOut={handlePressOut}
          style={styles.press}
        >
          <SymbolView
            name={listening ? 'waveform' : busy ? 'ellipsis' : 'mic.fill'}
            tintColor={live ? colors.ember : colors.text1}
            size={20}
          />
          {listening ? (
            <Waveform trace={trace} listening height={24} scale={0.62} />
          ) : (
            <Text style={[styles.label, live && styles.labelLive]} numberOfLines={1}>
              {label}
            </Text>
          )}
        </Pressable>

        {/* Silent path: the keyboard is a peer control, never a downgrade. */}
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Type instead"
          hitSlop={8}
          onPress={onKeyboard}
          style={({ pressed }) => [styles.keyboard, pressed && styles.pressed]}
        >
          <SymbolView name="keyboard" tintColor={colors.text3} size={18} />
        </Pressable>
      </Glass>
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: {
    paddingHorizontal: space[5],
    paddingBottom: space[2],
  },
  pill: {
    flexDirection: 'row',
    alignItems: 'center',
    minHeight: 56,
    paddingLeft: space[5],
    paddingRight: space[3],
    gap: space[2],
  },
  press: {
    flex: 1,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    minHeight: hitMin,
  },
  label: {
    ...type.bodyMedium,
    color: colors.text1,
    flexShrink: 1,
  },
  labelLive: {
    color: colors.ember,
  },
  keyboard: {
    width: hitMin,
    height: hitMin,
    alignItems: 'center',
    justifyContent: 'center',
  },
  pressed: {
    opacity: 0.6,
  },
});
