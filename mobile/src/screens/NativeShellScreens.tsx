import React from 'react';
import { Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';
import { SymbolView, type SFSymbol } from 'expo-symbols';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useNavigation } from '@react-navigation/native';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import type { RootStackParamList } from '../navigation/types';
import { colors, radius, space, type } from '../theme/tokens';

type ShellNavigation = NativeStackNavigationProp<RootStackParamList>;
type Destination = {
  route: keyof RootStackParamList;
  label: string;
  detail: string;
  icon: SFSymbol;
};

const workDestinations: Destination[] = [
  { route: 'NewConversation', label: 'New conversation', detail: 'Start a private Scout chat or a public channel.', icon: 'square.and.pencil' },
  { route: 'Deck', label: 'Threads, rooms, and work', detail: 'Return to the live company deck.', icon: 'bubble.left.and.bubble.right.fill' },
  { route: 'Meetings', label: 'Meetings', detail: 'Open permanent meeting records and exact sources.', icon: 'doc.text.magnifyingglass' },
  { route: 'Files', label: 'Files', detail: 'Documents and artifacts from the work.', icon: 'folder.fill' },
  { route: 'AgentTeam', label: 'Agent team', detail: 'Private agent work and accountable controls.', icon: 'person.2.fill' },
];

const networkDestinations: Destination[] = [
  { route: 'NetworkDraft', label: 'Private draft', detail: 'Edit your private projection while publication and discovery stay off.', icon: 'square.and.pencil' },
];

const youDestinations: Destination[] = [
  { route: 'Profile', label: 'Profile', detail: 'Your server-authorized identity.', icon: 'person.crop.circle.fill' },
  { route: 'WorkRecord', label: 'Work Record', detail: 'Private contribution evidence and controls.', icon: 'checkmark.seal.fill' },
  { route: 'Organizations', label: 'Organizations', detail: 'Memberships and current organization.', icon: 'building.2.fill' },
  { route: 'Settings', label: 'Settings', detail: 'Account, voice, and appearance.', icon: 'gearshape.fill' },
];

export function WorkHomeScreen() {
  return <DestinationScreen title="Work" subtitle="Conversation, meetings, and the things they produce." destinations={workDestinations} />;
}

export function NetworkHomeScreen() {
  return (
    <DestinationScreen
      title="Network"
      subtitle="A private draft is available. Every public network path remains off."
      destinations={networkDestinations}
      notice={{ title: 'Public network unavailable', body: 'Preview, publication, blocks, workstream, moderation, and public presence are parent-off. No public child surface or fixture data is mounted.' }}
    />
  );
}

export function WorkSearchHomeScreen() {
  return (
    <DestinationScreen
      title="Work Search"
      subtitle="Search is governed discovery, not a people directory."
      destinations={[]}
      notice={{ title: 'Work Search unavailable', body: 'W6 qualification is off. Interpretation, retrieval, contact, and result components are not mounted.' }}
    />
  );
}

export function YouHomeScreen() {
  return (
    <DestinationScreen
      title="You"
      subtitle="Identity, organizations, work evidence, and private controls."
      destinations={youDestinations}
      notice={{ title: 'MyMind unavailable', body: 'W5 custody is off. No private MyMind child, body, or reconstructed history is mounted.' }}
    />
  );
}

function DestinationScreen({
  title,
  subtitle,
  destinations,
  notice,
}: {
  title: string;
  subtitle: string;
  destinations: Destination[];
  notice?: { title: string; body: string };
}) {
  const navigation = useNavigation<ShellNavigation>();
  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right']}>
      <ScrollView
        contentContainerStyle={styles.body}
        keyboardDismissMode="on-drag"
        keyboardShouldPersistTaps="handled"
        showsVerticalScrollIndicator={false}
      >
        <Text accessibilityRole="header" style={styles.title}>{title}</Text>
        <Text style={styles.subtitle}>{subtitle}</Text>
        {notice ? (
          <View accessibilityLabel={`${notice.title}. ${notice.body}`} accessibilityRole="summary" style={styles.notice}>
            <SymbolView name="lock.fill" size={18} tintColor={colors.text2} />
            <View style={styles.copy}>
              <Text style={styles.noticeTitle}>{notice.title}</Text>
              <Text style={styles.noticeBody}>{notice.body}</Text>
            </View>
          </View>
        ) : null}
        <View style={styles.list}>
          {destinations.map((destination) => (
            <Pressable
              key={destination.label}
              accessibilityHint={destination.detail}
              accessibilityLabel={destination.label}
              accessibilityRole="button"
              onPress={() => navigation.navigate(destination.route as never)}
              style={({ pressed }) => [styles.row, pressed && styles.pressed]}
            >
              <View style={styles.icon}>
                <SymbolView name={destination.icon} size={20} tintColor={colors.text1} />
              </View>
              <View style={styles.copy}>
                <Text style={styles.rowTitle}>{destination.label}</Text>
                <Text style={styles.rowDetail}>{destination.detail}</Text>
              </View>
              <SymbolView name="chevron.right" size={14} tintColor={colors.text3} />
            </Pressable>
          ))}
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  body: { width: '100%', maxWidth: 820, alignSelf: 'center', padding: space[5], paddingBottom: space[10] },
  title: { ...type.title1, color: colors.text1 },
  subtitle: { ...type.body, color: colors.text2, marginTop: space[2], maxWidth: 600 },
  notice: {
    marginTop: space[5],
    minHeight: 72,
    flexDirection: 'row',
    alignItems: 'flex-start',
    gap: space[3],
    padding: space[4],
    borderRadius: radius.lg,
    backgroundColor: colors.surface2,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.border,
  },
  noticeTitle: { ...type.bodyMedium, color: colors.text1 },
  noticeBody: { ...type.bodySm, color: colors.text2, marginTop: 2 },
  list: { gap: space[2], marginTop: space[5] },
  row: {
    minHeight: 72,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    padding: space[3],
    borderRadius: radius.lg,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.border,
  },
  icon: { width: 44, height: 44, borderRadius: radius.md, alignItems: 'center', justifyContent: 'center', backgroundColor: colors.surface3 },
  copy: { flex: 1, minWidth: 0 },
  rowTitle: { ...type.bodyMedium, color: colors.text1 },
  rowDetail: { ...type.bodySm, color: colors.text2, marginTop: 2 },
  pressed: { opacity: 0.84, transform: [{ scale: 0.96 }] },
});
