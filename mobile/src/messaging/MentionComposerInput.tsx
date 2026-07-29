import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Animated, Pressable, StyleSheet, Text, TextInput, View } from 'react-native';
import * as Haptics from 'expo-haptics';
import { SymbolView } from 'expo-symbols';

import type { ChatMentionCandidate } from '../api/types';
import { colors, radius, space, type } from '../theme/tokens';
import { activeMentionQuery, completeMention } from './messagePresentation';
import { compactComposerHeight, composerHeight } from './composerMeasurement';
import { useReduceMotion } from '../theme/motion';

type Props = {
  value: string;
  onChangeText: (value: string) => void;
  onBlur?: () => void;
  candidates: ChatMentionCandidate[];
  placeholder: string;
  editable: boolean;
};

type DraftSegment = { text: string; mention?: ChatMentionCandidate };

function draftSegments(value: string, candidates: ChatMentionCandidate[]): DraftSegment[] {
  const byName = new Map(candidates.map((candidate) => [candidate.name.toLowerCase(), candidate]));
  const result: DraftSegment[] = [];
  const pattern = /@([\p{L}\p{N}]+)/gu;
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

export function MentionComposerInput({ value, onChangeText, onBlur, candidates, placeholder, editable }: Props) {
  const reduceMotion = useReduceMotion();
  const shimmer = useRef(new Animated.Value(1)).current;
  const [measuredHeight, setMeasuredHeight] = useState(compactComposerHeight);
  const active = useMemo(() => activeMentionQuery(value), [value]);
  const suggestions = useMemo(() => {
    if (!active) return [];
    const query = active.query.toLowerCase();
    return candidates.filter((candidate) => candidate.name.toLowerCase().startsWith(query)).slice(0, 5);
  }, [active, candidates]);
  const segments = useMemo(() => draftSegments(value, candidates), [candidates, value]);

  useEffect(() => {
    if (!value) setMeasuredHeight(compactComposerHeight);
  }, [value]);

  function select(candidate: ChatMentionCandidate) {
    onChangeText(completeMention(value, candidate.name));
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
              key={candidate.email || candidate.kind}
              accessibilityRole="button"
              accessibilityLabel={`Mention ${candidate.name}`}
              onPress={() => select(candidate)}
              style={({ pressed }) => [styles.suggestion, pressed && styles.suggestionPressed]}
            >
              <View style={[styles.avatar, candidate.kind === 'scout' && styles.avatarScout]}>
                <SymbolView name={candidate.kind === 'scout' ? 'sparkles' : 'person.fill'} tintColor={candidate.kind === 'scout' ? colors.emberText : colors.info} size={13} />
              </View>
              <Text style={[styles.suggestionName, candidate.kind === 'scout' && styles.suggestionScout]}>{candidate.name}</Text>
              <Text style={styles.suggestionKind}>{candidate.kind === 'scout' ? 'AI teammate' : 'Notify'}</Text>
            </Pressable>
          ))}
        </View>
      ) : null}
      <View style={[styles.frame, { height: measuredHeight }]}>
        {value ? (
          <Animated.Text pointerEvents="none" style={[styles.overlay, { opacity: shimmer }]}>
            {segments.map((segment, index) => segment.mention ? (
              <Text key={index}>
                <Text style={styles.hiddenAt}>@</Text>
                <Text style={[styles.mention, segment.mention.kind === 'scout' && styles.mentionScout]}>{segment.mention.name}</Text>
              </Text>
            ) : <React.Fragment key={index}>{segment.text}</React.Fragment>)}
          </Animated.Text>
        ) : null}
        <TextInput
          accessibilityLabel="Message"
          placeholder={placeholder}
          placeholderTextColor={colors.text3}
          selectionColor={colors.info}
          value={value}
          onChangeText={onChangeText}
          onBlur={onBlur}
          onContentSizeChange={(event) => {
            setMeasuredHeight(composerHeight(value, event.nativeEvent.contentSize.height));
          }}
          multiline
          editable={editable}
          scrollEnabled={measuredHeight >= 132}
          style={[styles.input, { height: measuredHeight }, value ? styles.inputTransparent : null]}
        />
      </View>
    </View>
  );
}

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
  mention: { fontWeight: '600', color: colors.info, backgroundColor: colors.infoSoft, textShadowColor: 'rgba(10,132,255,0.24)', textShadowRadius: 5 },
  mentionScout: { color: colors.emberText, backgroundColor: colors.emberSoft, textShadowColor: 'rgba(255,107,74,0.24)' },
});
