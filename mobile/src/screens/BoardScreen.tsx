import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Alert,
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  SafeAreaView,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import * as Haptics from 'expo-haptics';
import { useFocusEffect } from '@react-navigation/native';
import { api, BonfireApiError } from '../api/client';
import type { BoardCard, BoardCardInput, BoardResponse } from '../api/types';
import { useAuth } from '../auth/AuthContext';
import { Card } from '../components/Card';
import { Screen } from '../components/Screen';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { colors, space, type } from '../theme/tokens';

function cardColumn(card: BoardCard): string {
  return String(card.column || card.status || 'backlog').toLowerCase();
}

function cardTitle(card: BoardCard): string {
  return String(card.title || card.notes || card.body || card.id || 'Untitled');
}

const statusChoices = ['Backlog', 'In progress', 'Blocked', 'Done'];
const deliveryStages = [
  { id: 'requested', label: 'Work requested' },
  { id: 'delivered', label: 'Work delivered' },
  { id: 'drive', label: 'Saved to Drive' },
] as const;
type BoardProjection = NonNullable<BoardResponse['projection']>;

type CardEditor = {
  id?: string;
  title: string;
  status: string;
  owner: string;
  notes: string;
  tags: string;
  dueDate: string;
};

function editorForCard(card?: BoardCard): CardEditor {
  return {
    id: card?.id,
    title: cardTitle(card ?? { id: '' }) === 'Untitled' ? '' : cardTitle(card ?? { id: '' }),
    status: String(card?.status || card?.column || 'Backlog'),
    owner: String(card?.owner || 'Unassigned'),
    notes: String(card?.notes || card?.body || ''),
    tags: (Array.isArray(card?.tags) ? card.tags : Array.isArray(card?.labels) ? card.labels : []).join(', '),
    dueDate: String(card?.dueDate || ''),
  };
}

export function BoardScreen() {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const [cards, setCards] = useState<BoardCard[]>([]);
  const [updatedAt, setUpdatedAt] = useState<string | undefined>();
  const [projection, setProjection] = useState<BoardProjection>({ cards: [], projects: [] });
  const [projectFilter, setProjectFilter] = useState('all');
  const [projectPickerOpen, setProjectPickerOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [resolvingDraft, setResolvingDraft] = useState<string | null>(null);
  const [editor, setEditor] = useState<CardEditor | null>(null);
  const [saving, setSaving] = useState(false);

  const load = useCallback(
    async (mode: 'initial' | 'refresh' = 'initial') => {
      if (!sessionToken) return;
      if (mode === 'refresh') setRefreshing(true);
      else setLoading(true);
      setError(null);
      try {
        const res = await api.board(sessionToken);
        setCards(res.board?.cards ?? []);
        setUpdatedAt(res.board?.updatedAt);
        setProjection(res.projection ?? { cards: [], projects: [] });
      } catch (err) {
        setError(err instanceof BonfireApiError ? err.message : 'Could not load board');
      } finally {
        setLoading(false);
        setRefreshing(false);
      }
    },
    [sessionToken],
  );

  useFocusEffect(
    useCallback(() => {
      void load('initial');
    }, [load]),
  );

  useEffect(() => {
    if (office.event === 'board') void load('refresh');
  }, [load, office.event, office.version]);

  const projectionByCard = useMemo(
    () => new Map((projection.cards ?? []).map((row) => [row.cardId, row])),
    [projection.cards],
  );
  const projectOptions = useMemo(
    () => [{ id: 'all', title: 'All projects' }, ...(projection.projects ?? [])],
    [projection.projects],
  );
  const selectedProject = projectOptions.find((project) => project.id === projectFilter) ?? projectOptions[0];
  const grouped = useMemo(
    () => deliveryStages.map((stage) => ({
      ...stage,
      cards: cards.filter((card) => {
        const row = projectionByCard.get(card.id);
        const fallbackStage = cardColumn(card) === 'done' ? 'delivered' : 'requested';
        return (row?.deliveryStage ?? fallbackStage) === stage.id
          && (projectFilter === 'all' || row?.projectId === projectFilter);
      }),
    })),
    [cards, projectFilter, projectionByCard],
  );

  useEffect(() => {
    if (projectFilter !== 'all' && !projectOptions.some((project) => project.id === projectFilter)) {
      setProjectFilter('all');
    }
  }, [projectFilter, projectOptions]);

  async function resolveDraft(card: BoardCard, action: 'accept' | 'dismiss', reason = '') {
    if (!sessionToken || resolvingDraft) return;
    setResolvingDraft(card.id);
    setError(null);
    try {
      await api.resolveBoardDraft(sessionToken, card.id, action, reason);
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
      await load('refresh');
    } catch (err) {
      setError(err instanceof BonfireApiError ? err.message : 'Could not resolve this draft.');
    } finally {
      setResolvingDraft(null);
    }
  }

  function dismissDraft(card: BoardCard) {
    Alert.prompt('Dismiss Scout’s draft?', 'Optional: tell Scout why so it remembers.', (reason) => {
      void resolveDraft(card, 'dismiss', reason);
    });
  }

  async function saveCard() {
    if (!sessionToken || !editor || saving) return;
    if (!editor.title.trim()) {
      Alert.alert('Give this card a title', 'A short, action-oriented title works best.');
      return;
    }
    const payload: BoardCardInput = {
      title: editor.title.trim(),
      status: editor.status,
      owner: editor.owner.trim() || 'Unassigned',
      notes: editor.notes.trim(),
      tags: editor.tags.split(',').map((tag) => tag.trim()).filter(Boolean),
      dueDate: editor.dueDate.trim(),
    };
    setSaving(true);
    setError(null);
    try {
      if (editor.id) await api.updateBoardCard(sessionToken, editor.id, payload);
      else await api.createBoardCard(sessionToken, payload);
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
      setEditor(null);
      await load('refresh');
    } catch (err) {
      const message = err instanceof BonfireApiError ? err.message : 'Could not save this card.';
      setError(message);
      Alert.alert('Card not saved', message);
    } finally {
      setSaving(false);
    }
  }

  function confirmDelete(card: BoardCard) {
    Alert.alert('Delete this card?', cardTitle(card), [
      { text: 'Cancel', style: 'cancel' },
      {
        text: 'Delete',
        style: 'destructive',
        onPress: () => {
          if (!sessionToken) return;
          setSaving(true);
          void api.deleteBoardCard(sessionToken, card.id)
            .then(async () => {
              await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
              setEditor(null);
              await load('refresh');
            })
            .catch((err) => {
              const message = err instanceof BonfireApiError ? err.message : 'Could not delete this card.';
              setError(message);
              Alert.alert('Card not deleted', message);
            })
            .finally(() => setSaving(false));
        },
      },
    ]);
  }

  async function undoDelete() {
    if (!sessionToken || saving) return;
    setSaving(true);
    setError(null);
    try {
      await api.undoDeleteBoardCard(sessionToken);
      await Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
      await load('refresh');
    } catch (err) {
      const message = err instanceof BonfireApiError ? err.message : 'There is no recent card to restore.';
      setError(message);
    } finally {
      setSaving(false);
    }
  }

  return (
    <Screen
      title="Board"
      subtitle={
        updatedAt
          ? `Project delivery · updated ${new Date(updatedAt).toLocaleString()}`
          : 'Project delivery across requested, delivered, and Drive'
      }
      loading={loading}
      error={error}
      onRetry={() => void load('initial')}
      refreshing={refreshing}
      onRefresh={() => void load('refresh')}
    >
      <View style={styles.boardActions}>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Create board card"
          accessibilityHint="Opens the native card editor."
          accessibilityState={{ disabled: saving }}
          disabled={saving}
          onPress={() => setEditor(editorForCard())}
          style={({ pressed }) => [styles.primaryAction, pressed && styles.pressed]}
        >
          <Text style={styles.primaryActionText}>＋ New card</Text>
        </Pressable>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Undo last card deletion"
          accessibilityState={{ disabled: saving }}
          disabled={saving}
          onPress={() => void undoDelete()}
          style={({ pressed }) => [styles.secondaryAction, pressed && styles.pressed]}
        >
          <Text style={styles.secondaryActionText}>Undo delete</Text>
        </Pressable>
      </View>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={`Filter Board by project. Current selection ${selectedProject.title}`}
        accessibilityState={{ expanded: projectPickerOpen }}
        onPress={() => setProjectPickerOpen(true)}
        style={({ pressed }) => [styles.projectSelect, pressed && styles.pressed]}
      >
        <View>
          <Text style={styles.projectSelectLabel}>PROJECT</Text>
          <Text style={styles.projectSelectValue}>{selectedProject.title}</Text>
        </View>
        <Text style={styles.projectSelectChevron}>⌄</Text>
      </Pressable>
      {cards.length === 0 && !loading ? (
        <Text style={styles.empty}>No board cards yet.</Text>
      ) : (
        grouped.map((stage) => (
          <View key={stage.id} style={styles.section}>
            <Text style={styles.sectionTitle}>
              {stage.label} · {stage.cards.length}
            </Text>
            {stage.cards.length === 0 ? <Text style={styles.stageEmpty}>Nothing here yet</Text> : null}
            {stage.cards.map((card) => (
              <View key={String(card.id)}>
                <Card
                title={cardTitle(card)}
                subtitle={
                  typeof (card.notes || card.body) === 'string' && (card.notes || card.body) !== cardTitle(card)
                    ? (card.notes || card.body)
                    : undefined
                }
                meta={
                  [
                    card.owner,
                    projectionByCard.get(card.id)?.projectTitle,
                    String(card.status || card.column || ''),
                    Array.isArray(card.tags) ? card.tags.join(' · ') : Array.isArray(card.labels) ? card.labels.join(' · ') : '',
                    card.dueDate ? `due ${new Date(card.dueDate).toLocaleDateString()}` : '',
                  ]
                    .filter(Boolean)
                    .join(' · ') || undefined
                }
                badge={card.draft ? 'draft' : undefined}
                badgeTone={card.draft ? 'warn' : 'muted'}
                onPress={card.draft ? undefined : () => setEditor(editorForCard(card))}
                accessibilityHint={card.draft ? undefined : 'Opens the native card editor.'}
              />
                {card.draft ? (
                  <View style={styles.draftActions}>
                    <Pressable
                      accessibilityRole="button"
                      onPress={() => void resolveDraft(card, 'accept')}
                      disabled={Boolean(resolvingDraft)}
                      style={({ pressed }) => [styles.accept, pressed && styles.pressed]}
                    >
                      <Text style={styles.acceptText}>Add to board</Text>
                    </Pressable>
                    <Pressable
                      accessibilityRole="button"
                      onPress={() => dismissDraft(card)}
                      disabled={Boolean(resolvingDraft)}
                      style={({ pressed }) => [styles.dismiss, pressed && styles.pressed]}
                    >
                      <Text style={styles.dismissText}>Dismiss</Text>
                    </Pressable>
                  </View>
                ) : null}
              </View>
            ))}
          </View>
        ))
      )}
      <Modal
        visible={projectPickerOpen}
        animationType="slide"
        presentationStyle="pageSheet"
        onRequestClose={() => setProjectPickerOpen(false)}
      >
        <SafeAreaView style={styles.modalSafe}>
          <View style={styles.modalHeader}>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Close project filter"
              onPress={() => setProjectPickerOpen(false)}
              style={styles.modalHeaderButton}
            >
              <Text style={styles.modalCancel}>Close</Text>
            </Pressable>
            <Text style={styles.modalTitle}>Project</Text>
            <View style={styles.modalHeaderButton} />
          </View>
          <ScrollView contentContainerStyle={styles.projectOptions}>
            {projectOptions.map((project) => {
              const selected = project.id === projectFilter;
              return (
                <Pressable
                  key={project.id}
                  accessibilityRole="button"
                  accessibilityState={{ selected }}
                  onPress={() => {
                    setProjectFilter(project.id);
                    setProjectPickerOpen(false);
                  }}
                  style={({ pressed }) => [styles.projectOption, selected && styles.projectOptionSelected, pressed && styles.pressed]}
                >
                  <Text style={[styles.projectOptionText, selected && styles.projectOptionTextSelected]}>{project.title}</Text>
                  {selected ? <Text style={styles.projectOptionCheck}>✓</Text> : null}
                </Pressable>
              );
            })}
          </ScrollView>
        </SafeAreaView>
      </Modal>
      <Modal
        visible={Boolean(editor)}
        animationType="slide"
        presentationStyle="pageSheet"
        onRequestClose={() => !saving && setEditor(null)}
      >
        <SafeAreaView style={styles.modalSafe}>
          <KeyboardAvoidingView
            style={styles.modalSafe}
            behavior={Platform.OS === 'ios' ? 'padding' : undefined}
          >
            <View style={styles.modalHeader}>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Cancel card editing"
                disabled={saving}
                onPress={() => setEditor(null)}
                style={styles.modalHeaderButton}
              >
                <Text style={styles.modalCancel}>Cancel</Text>
              </Pressable>
              <Text style={styles.modalTitle}>{editor?.id ? 'Edit card' : 'New card'}</Text>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel="Save board card"
                accessibilityState={{ busy: saving, disabled: saving }}
                disabled={saving}
                onPress={() => void saveCard()}
                style={styles.modalHeaderButton}
              >
                <Text style={styles.modalSave}>{saving ? 'Saving…' : 'Save'}</Text>
              </Pressable>
            </View>
            <ScrollView
              style={styles.editorScroll}
              contentContainerStyle={styles.editorContent}
              keyboardShouldPersistTaps="handled"
            >
              <Text style={styles.fieldLabel}>TITLE</Text>
              <TextInput
                accessibilityLabel="Card title"
                autoFocus={!editor?.id}
                value={editor?.title ?? ''}
                onChangeText={(title) => setEditor((current) => current ? { ...current, title } : current)}
                placeholder="What needs to happen?"
                placeholderTextColor={colors.text3}
                style={styles.input}
              />
              <Text style={styles.fieldLabel}>STATUS</Text>
              <View style={styles.statusChoices}>
                {statusChoices.map((status) => {
                  const selected = editor?.status.toLowerCase() === status.toLowerCase();
                  return (
                    <Pressable
                      key={status}
                      accessibilityRole="button"
                      accessibilityLabel={`Set status to ${status}`}
                      accessibilityState={{ selected }}
                      onPress={() => setEditor((current) => current ? { ...current, status } : current)}
                      style={[styles.statusChoice, selected && styles.statusChoiceSelected]}
                    >
                      <Text style={[styles.statusChoiceText, selected && styles.statusChoiceTextSelected]}>{status}</Text>
                    </Pressable>
                  );
                })}
              </View>
              <Text style={styles.fieldLabel}>OWNER</Text>
              <TextInput
                accessibilityLabel="Card owner"
                value={editor?.owner ?? ''}
                onChangeText={(owner) => setEditor((current) => current ? { ...current, owner } : current)}
                placeholder="Unassigned"
                placeholderTextColor={colors.text3}
                style={styles.input}
              />
              <Text style={styles.fieldLabel}>DUE DATE</Text>
              <TextInput
                accessibilityLabel="Card due date"
                value={editor?.dueDate ?? ''}
                onChangeText={(dueDate) => setEditor((current) => current ? { ...current, dueDate } : current)}
                placeholder="Jul 31 or 2026-07-31"
                placeholderTextColor={colors.text3}
                style={styles.input}
              />
              <Text style={styles.fieldLabel}>TAGS</Text>
              <TextInput
                accessibilityLabel="Card tags"
                value={editor?.tags ?? ''}
                onChangeText={(tags) => setEditor((current) => current ? { ...current, tags } : current)}
                placeholder="mobile, release"
                placeholderTextColor={colors.text3}
                style={styles.input}
              />
              <Text style={styles.fieldLabel}>NOTES</Text>
              <TextInput
                accessibilityLabel="Card notes"
                value={editor?.notes ?? ''}
                onChangeText={(notes) => setEditor((current) => current ? { ...current, notes } : current)}
                placeholder="Context, next step, or acceptance criteria"
                placeholderTextColor={colors.text3}
                multiline
                textAlignVertical="top"
                style={[styles.input, styles.notesInput]}
              />
              {editor?.id ? (
                <Pressable
                  accessibilityRole="button"
                  accessibilityLabel="Delete board card"
                  accessibilityState={{ disabled: saving }}
                  disabled={saving}
                  onPress={() => {
                    const card = cards.find((candidate) => candidate.id === editor.id);
                    if (card) confirmDelete(card);
                  }}
                  style={({ pressed }) => [styles.deleteButton, pressed && styles.pressed]}
                >
                  <Text style={styles.deleteButtonText}>Delete card</Text>
                </Pressable>
              ) : null}
            </ScrollView>
          </KeyboardAvoidingView>
        </SafeAreaView>
      </Modal>
    </Screen>
  );
}

const styles = StyleSheet.create({
  empty: {
    ...type.bodySm,
    color: colors.text2,
  },
  section: {
    marginBottom: space[2],
  },
  stageEmpty: { ...type.bodySm, color: colors.text3, paddingHorizontal: space[2], paddingBottom: space[3] },
  boardActions: { flexDirection: 'row', gap: space[2], marginBottom: space[4] },
  primaryAction: { flex: 1, minHeight: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 12, backgroundColor: colors.accent },
  primaryActionText: { ...type.button, color: colors.onAccent },
  secondaryAction: { minHeight: 44, paddingHorizontal: space[4], alignItems: 'center', justifyContent: 'center', borderRadius: 12, backgroundColor: colors.surface3 },
  secondaryActionText: { ...type.button, color: colors.text2 },
  projectSelect: {
    minHeight: 58,
    marginBottom: space[4],
    paddingHorizontal: space[4],
    borderRadius: 16,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.glassBorder,
    backgroundColor: colors.glassPanel,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
  },
  projectSelectLabel: { ...type.label, color: colors.text3 },
  projectSelectValue: { ...type.bodyMedium, color: colors.text1, marginTop: 2 },
  projectSelectChevron: { ...type.headline, color: colors.text2 },
  projectOptions: { padding: space[5], gap: space[2] },
  projectOption: {
    minHeight: 52,
    paddingHorizontal: space[4],
    borderRadius: 14,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    backgroundColor: colors.surface1,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
  },
  projectOptionSelected: { borderColor: colors.ember, backgroundColor: colors.emberSoft },
  projectOptionText: { ...type.bodyMedium, color: colors.text1 },
  projectOptionTextSelected: { color: colors.emberText },
  projectOptionCheck: { ...type.bodyMedium, color: colors.emberText },
  sectionTitle: {
    ...type.label,
    color: colors.text3,
    marginBottom: space[2],
    marginTop: space[2],
    textTransform: 'uppercase',
  },
  draftActions: { flexDirection: 'row', gap: space[2], marginTop: -space[2], marginBottom: space[3], paddingHorizontal: space[2] },
  accept: { flex: 1, minHeight: 42, alignItems: 'center', justifyContent: 'center', borderRadius: 12, backgroundColor: colors.accent },
  acceptText: { ...type.button, color: colors.onAccent },
  dismiss: { flex: 1, minHeight: 42, alignItems: 'center', justifyContent: 'center', borderRadius: 12, backgroundColor: colors.surface3 },
  dismissText: { ...type.button, color: colors.text2 },
  pressed: { opacity: 0.75, transform: [{ scale: 0.98 }] },
  modalSafe: { flex: 1, backgroundColor: colors.bg },
  modalHeader: { minHeight: 56, paddingHorizontal: space[3], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' },
  modalHeaderButton: { minWidth: 72, minHeight: 44, justifyContent: 'center' },
  modalTitle: { ...type.headline, color: colors.text1 },
  modalCancel: { ...type.bodyMedium, color: colors.text2 },
  modalSave: { ...type.bodyMedium, color: colors.info, textAlign: 'right' },
  editorScroll: { flex: 1 },
  editorContent: { padding: space[5], paddingBottom: space[12], gap: space[2] },
  fieldLabel: { ...type.label, color: colors.text3, marginTop: space[3] },
  input: { minHeight: 48, borderRadius: 12, borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, backgroundColor: colors.surface1, color: colors.text1, ...type.body, paddingHorizontal: space[4], paddingVertical: space[3] },
  notesInput: { minHeight: 132 },
  statusChoices: { flexDirection: 'row', flexWrap: 'wrap', gap: space[2] },
  statusChoice: { minHeight: 44, paddingHorizontal: space[3], justifyContent: 'center', borderRadius: 999, backgroundColor: colors.surface3 },
  statusChoiceSelected: { backgroundColor: colors.accent },
  statusChoiceText: { ...type.button, color: colors.text2 },
  statusChoiceTextSelected: { color: colors.onAccent },
  deleteButton: { minHeight: 48, marginTop: space[5], alignItems: 'center', justifyContent: 'center', borderRadius: 12, backgroundColor: colors.dangerSoft },
  deleteButtonText: { ...type.button, color: colors.danger },
});
