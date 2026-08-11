import React, { forwardRef, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import { Animated, Pressable, StyleSheet, Text, TextInput, View } from 'react-native';
import * as Haptics from 'expo-haptics';
import { SymbolView } from 'expo-symbols';

import type { ChatMentionCandidate } from '../api/types';
import { colors, radius, space, type } from '../theme/tokens';
import { activeMentionQuery, completeMention } from './messagePresentation';
import {
  compactComposerHeight,
  composerHeight,
  expandedComposerMaxHeight,
} from './composerMeasurement';
import { useReduceMotion } from '../theme/motion';
import { activeDocumentQuery } from '../drive/driveModels';

type Props = {
  value: string;
  onChangeText: (value: string) => void;
  onBlur?: () => void;
  candidates: ChatMentionCandidate[];
  placeholder: string;
  accessibilityLabel?: string;
  editable: boolean;
  maxHeight?: number;
  onDocumentQuery?: (query: string) => void;
};

export type MentionComposerInputHandle = {
  focus: () => void;
};

type DraftSegment = { text: string; mention?: ChatMentionCandidate };

function draftSegments(value: string, candidates: ChatMentionCandidate[]): DraftSegment[] {
  const byName = new Map<string, ChatMentionCandidate>();
  candidates.forEach((candidate) => {
    byName.set(candidate.name.toLowerCase(), candidate);
    if (candidate.handle) byName.set(candidate.handle.toLowerCase(), candidate);
  });
  const result: DraftSegment[] = [];
  const pattern = /@([\p{L}\p{N}._-]+)/gu;
  let cursor = 0;
  for (const match of value.matchAll(pattern)) {
    const start = match.index ?? 0;
    if (start > cursor) result.push({ text: value.slice(cursor, start) });
    const candidate = byName.get(String(match[1]).toLowerCase());
    result.push(candidate ? { text: match[0], mention: candidate } : { text: match[0] });
    cursor = start + match[0].length;
  }
  if (cursor < value.length) result.push({ text: value.slice(cursor) });
  return result;
}

function mentionCandidateSubtitle(candidate: ChatMentionCandidate): string {
  const roleTitle = candidate.roleTitle?.trim();
  if (candidate.kind === 'scout') return `${roleTitle || 'Chief of staff'} · AI`;
  if (candidate.kind === 'agent') return `${roleTitle || 'Specialist'} · AI`;
  return roleTitle || 'Teammate';
}

export const MentionComposerInput = forwardRef<MentionComposerInputHandle, Props>(function MentionComposerInput({
  value,
  onChangeText,
  onBlur,
  candidates,
  placeholder,
  accessibilityLabel = 'Message',
  editable,
  maxHeight = expandedComposerMaxHeight,
  onDocumentQuery,
}, ref) {
  const reduceMotion = useReduceMotion();
  const shimmer = useRef(new Animated.Value(1)).current;
  const valueRef = useRef(value);
	const textInputRef = useRef<TextInput>(null);
  const inputWidthRef = useRef(0);
  const nativeContentHeightRef = useRef(compactComposerHeight);
  const [measuredHeight, setMeasuredHeight] = useState(compactComposerHeight);
  const [focused, setFocused] = useState(false);
  const active = useMemo(() => activeMentionQuery(value), [value]);
  const suggestions = useMemo(() => {
    if (!active) return [];
    const query = active.query.toLowerCase();
    return candidates.filter((candidate) => (
      candidate.name.toLowerCase().startsWith(query) || candidate.handle?.toLowerCase().startsWith(query)
    )).slice(0, 5);
  }, [active, candidates]);
  const segments = useMemo(() => draftSegments(value, candidates), [candidates, value]);
  const showMentionOverlay = !focused && segments.some((segment) => Boolean(segment.mention));

  valueRef.current = value;

	useImperativeHandle(ref, () => ({
		focus: () => textInputRef.current?.focus(),
	}), []);

  useEffect(() => {
    if (!value) {
      nativeContentHeightRef.current = compactComposerHeight;
      setMeasuredHeight(compactComposerHeight);
    }
  }, [value]);

  useEffect(() => {
    setMeasuredHeight((current) => Math.max(compactComposerHeight, Math.min(maxHeight, current)));
  }, [maxHeight]);

  function select(candidate: ChatMentionCandidate) {
    onChangeText(completeMention(value, candidate.handle || candidate.name));
    void Haptics.selectionAsync();
    if (reduceMotion) return;
    shimmer.setValue(0.58);
    Animated.timing(shimmer, { toValue: 1, duration: 360, useNativeDriver: true }).start();
  }

  return (
    <View>
      {suggestions.length > 0 ? (
        <View style={styles.suggestions} accessibilityLabel="Mention suggestions">
          {suggestions.map((candidate) => (
            <Pressable
              key={`${candidate.kind}:${candidate.email || candidate.name}`}
              accessibilityRole="button"
              accessibilityLabel={`Mention ${candidate.name}`}
              onPress={() => select(candidate)}
              style={({ pressed }) => [styles.suggestion, pressed && styles.suggestionPressed]}
            >
              <View style={[styles.avatar, candidate.kind !== 'person' && styles.avatarScout]}>
                <SymbolView name={candidate.kind !== 'person' ? 'sparkles' : 'person.fill'} tintColor={candidate.kind !== 'person' ? colors.emberText : colors.info} size={13} />
              </View>
              <Text style={[styles.suggestionName, candidate.kind !== 'person' && styles.suggestionScout]}>{candidate.name}</Text>
              <Text style={styles.suggestionKind}>{mentionCandidateSubtitle(candidate)}</Text>
            </Pressable>
          ))}
        </View>
      ) : null}
      <View style={[styles.frame, { height: measuredHeight, maxHeight }]}>
        {showMentionOverlay ? (
          <Animated.Text maxFontSizeMultiplier={2} pointerEvents="none" style={[styles.overlay, { opacity: shimmer }]}>
            {segments.map((segment, index) => segment.mention ? (
              <Text key={index}>
                <Text style={styles.hiddenAt}>@</Text>
                <Text style={[styles.mention, segment.mention.kind !== 'person' && styles.mentionScout]}>{segment.text.slice(1)}</Text>
              </Text>
            ) : <React.Fragment key={index}>{segment.text}</React.Fragment>)}
          </Animated.Text>
        ) : null}
        <TextInput
          maxFontSizeMultiplier={2}
		  ref={textInputRef}
          accessibilityLabel={accessibilityLabel}
          placeholder={placeholder}
          placeholderTextColor={colors.text3}
          selectionColor={colors.info}
          value={value}
          onChangeText={(nextValue) => {
            valueRef.current = nextValue;
            if (!nextValue) nativeContentHeightRef.current = compactComposerHeight;
            setMeasuredHeight(composerHeight(nextValue, nativeContentHeightRef.current, maxHeight, inputWidthRef.current));
            onChangeText(nextValue);
            const documentQuery = activeDocumentQuery(nextValue);
            if (documentQuery) onDocumentQuery?.(documentQuery.query);
          }}
          onFocus={() => setFocused(true)}
          onBlur={() => {
            setFocused(false);
            onBlur?.();
          }}
          onContentSizeChange={(event) => {
            nativeContentHeightRef.current = event.nativeEvent.contentSize.height;
            setMeasuredHeight(composerHeight(valueRef.current, nativeContentHeightRef.current, maxHeight, inputWidthRef.current));
          }}
          onLayout={(event) => {
            inputWidthRef.current = event.nativeEvent.layout.width;
            setMeasuredHeight(composerHeight(valueRef.current, nativeContentHeightRef.current, maxHeight, inputWidthRef.current));
          }}
          multiline
          editable={editable}
          scrollEnabled={measuredHeight >= maxHeight}
          style={[styles.input, { height: measuredHeight, maxHeight }, showMentionOverlay ? styles.inputTransparent : null]}
        />
      </View>
    </View>
  );
});

const styles = StyleSheet.create({
  suggestions: { gap: 3, marginBottom: space[2], padding: 4, borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, backgroundColor: colors.surface1 },
  suggestion: { minHeight: 42, flexDirection: 'row', alignItems: 'center', gap: space[2], paddingHorizontal: space[2], borderRadius: radius.md },
  suggestionPressed: { backgroundColor: colors.surface3, transform: [{ scale: 0.99 }] },
  avatar: { width: 28, height: 28, alignItems: 'center', justifyContent: 'center', borderRadius: 14, backgroundColor: colors.infoSoft },
  avatarScout: { backgroundColor: colors.emberSoft },
  suggestionName: { ...type.bodyMedium, color: colors.text1 },
  suggestionScout: { color: colors.emberText },
  suggestionKind: { ...type.caption, flex: 1, textAlign: 'right', color: colors.text3 },
  frame: { minHeight: 40, maxHeight: 132, justifyContent: 'flex-start' },
  overlay: { ...type.body, position: 'absolute', top: 0, left: 0, right: 0, paddingTop: 0, color: colors.text1 },
  input: { minHeight: 40, maxHeight: 132, padding: 0, ...type.body, color: colors.text1, textAlignVertical: 'top' },
  inputTransparent: { color: 'transparent' },
  hiddenAt: { color: 'transparent' },
  mention: { fontFamily: 'GoogleSansFlex_600SemiBold', fontWeight: '600', color: colors.info, backgroundColor: colors.infoSoft, textShadowColor: 'rgba(10,132,255,0.24)', textShadowRadius: 5 },
  mentionScout: { color: colors.emberText, backgroundColor: colors.emberSoft, textShadowColor: 'rgba(255,90,25,0.24)' },
});
