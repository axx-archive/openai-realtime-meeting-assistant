import React from 'react';
import { StyleSheet, Text, View } from 'react-native';
import * as Linking from 'expo-linking';
import { useMappingHelper } from '@shopify/flash-list';

import { colors, radius, space, type } from '../theme/tokens';
import { parseScoutMarkdown, truncateScoutBlocks, type ScoutInline } from './scoutRichTextPresentation';

function InlineContent({ inlines }: { inlines: ScoutInline[] }) {
  const { getMappingKey } = useMappingHelper();
  return (
    <Text>
      {inlines.map((inline, index) => {
        const mappingKey = getMappingKey(`${inline.kind}-${inline.text}`, index);
        if (inline.kind === 'link' && inline.url) {
          return (
            <Text
              key={mappingKey}
              accessibilityRole="link"
              onPress={() => void Linking.openURL(inline.url!).catch(() => undefined)}
              style={styles.link}
            >
              {inline.text}
            </Text>
          );
        }
        if (inline.kind === 'mention') {
          return <Text key={mappingKey} style={[styles.mention, inline.scout && styles.mentionScout]}>{inline.text}</Text>;
        }
        return <Text key={mappingKey} style={inline.kind === 'strong' ? styles.strong : inline.kind === 'emphasis' ? styles.emphasis : inline.kind === 'code' ? styles.inlineCode : undefined}>{inline.text}</Text>;
      })}
    </Text>
  );
}

export function ScoutRichText({ text, maxCharacters }: { text: string; maxCharacters?: number }) {
  const { getMappingKey } = useMappingHelper();
  const parsed = React.useMemo(() => parseScoutMarkdown(text), [text]);
  const blocks = React.useMemo(
    () => typeof maxCharacters === 'number' ? truncateScoutBlocks(parsed, maxCharacters).blocks : parsed,
    [maxCharacters, parsed],
  );
  return (
    <View style={styles.root}>
      {blocks.map((block, index) => {
        const content = <InlineContent inlines={block.inlines} />;
        if (block.kind === 'bullet' || block.kind === 'number') {
          return (
            <View key={getMappingKey(`${block.kind}-${block.marker ?? ''}`, index)} style={styles.listRow}>
              <Text style={styles.marker}>{block.marker}</Text>
              <Text style={styles.body}>{content}</Text>
            </View>
          );
        }
        if (block.kind === 'quote') {
          return (
            <View key={getMappingKey(`${block.kind}-${index}`, index)} style={styles.quote}>
              <View style={styles.quoteLine} />
              <Text style={styles.quoteText}>{content}</Text>
            </View>
          );
        }
        if (block.kind === 'code') return <Text key={getMappingKey(`${block.kind}-${index}`, index)} style={styles.codeBlock}>{content}</Text>;
        if (block.kind === 'heading') return <Text key={getMappingKey(`${block.kind}-${block.level ?? 0}`, index)} style={[styles.heading, Boolean(block.level && block.level > 2) && styles.headingSmall]}>{content}</Text>;
        return <Text key={getMappingKey(`${block.kind}-${index}`, index)} style={styles.body}>{content}</Text>;
      })}
    </View>
  );
}

const styles = StyleSheet.create({
  root: { gap: 7 },
  body: { ...type.body, color: colors.text1, flexShrink: 1 },
  heading: { ...type.bodyMedium, marginTop: 3, color: colors.text1 },
  headingSmall: { ...type.captionMedium, color: colors.text2, textTransform: 'uppercase', letterSpacing: 0.4 },
  listRow: { flexDirection: 'row', alignItems: 'flex-start', gap: space[2] },
  marker: { ...type.bodyMedium, width: 16, color: colors.emberText, textAlign: 'center' },
  quote: { flexDirection: 'row', gap: space[2], paddingVertical: 2 },
  quoteLine: { width: 2, borderRadius: radius.full, backgroundColor: colors.ember },
  quoteText: { ...type.body, flex: 1, color: colors.text2 },
  codeBlock: { ...type.caption, padding: space[2], overflow: 'hidden', borderRadius: radius.sm, backgroundColor: colors.surface3, color: colors.text1, fontFamily: 'GeistMono_400Regular' },
  strong: { fontFamily: 'GoogleSansFlex_700Bold', fontWeight: '700' },
  emphasis: { fontStyle: 'italic' },
  inlineCode: { ...type.caption, color: colors.text1, backgroundColor: colors.surface3, fontFamily: 'GeistMono_400Regular' },
  link: { color: colors.info, textDecorationLine: 'underline' },
  mention: { ...type.bodyMedium, color: colors.info },
  mentionScout: { color: colors.emberText },
});
