import React, { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  Pressable,
  StyleSheet,
  Text,
  View,
  useWindowDimensions,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { useFocusEffect, useIsFocused } from '@react-navigation/native';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import { FlashList } from '@shopify/flash-list';
import { SymbolView } from 'expo-symbols';
import * as Haptics from 'expo-haptics';

import { api, BonfireApiError } from '../api/client';
import type { StudioProject, StudioProjectCheckpoint } from '../api/types';
import { artifactStudioIntent, artifactStudioPath } from '../artifacts/studioRoutes';
import { useAuth } from '../auth/AuthContext';
import type { RootStackParamList } from '../navigation/types';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { colors, hitMin, radius, shadow, space, type } from '../theme/tokens';
import { WorkProjectDetail, WorkProjectSheet } from '../work/WorkProjectSheet';
import {
  studioProjectFilters,
  studioProjectKindLabel,
  studioProjectListRows,
  studioProjectOpenTarget,
  studioProjectRelativeTime,
  studioProjectStatusLabel,
  type StudioProjectFilter,
  type StudioProjectListRow,
} from '../work/studioProjectModel';

type Props =
  | NativeStackScreenProps<RootStackParamList, 'WorkHome'>
  | NativeStackScreenProps<RootStackParamList, 'Board'>;
type CheckpointOption = NonNullable<StudioProjectCheckpoint['options']>[number];

const WORK_HUB_SPLIT_WIDTH = 744;

function projectSignalStyle(project: StudioProject) {
  if (project.status === 'ready') return styles.projectSignalReady;
  if (project.status === 'needs_input' || project.status === 'needs_attention') return styles.projectSignalAttention;
  if (project.status === 'stopped') return styles.projectSignalStopped;
  return styles.projectSignalActive;
}

const ProjectRow = memo(function ProjectRow({
  project,
  selected,
  onPress,
}: {
  project: StudioProject;
  selected: boolean;
  onPress: (project: StudioProject) => void;
}) {
  const active = project.status === 'queued' || project.status === 'running';
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={`${studioProjectKindLabel(project.kind)}. ${project.title}. ${studioProjectStatusLabel(project.status)}${active ? `, ${project.progressPercent}% complete` : ''}`}
      accessibilityState={{ selected }}
      onPress={() => onPress(project)}
      style={({ pressed }) => [styles.projectRow, selected && styles.projectRowSelected, pressed && styles.projectRowPressed]}
    >
      <View style={styles.projectIcon}>
        <SymbolView name={project.kind === 'presentation' ? 'rectangle.stack.fill' : 'doc.text.fill'} tintColor={colors.emberText} size={18} />
      </View>
      <View style={styles.projectCopy}>
        <View style={styles.projectMeta}>
          <Text style={styles.projectKind}>{studioProjectKindLabel(project.kind)}</Text>
          <Text style={styles.projectTime}>{studioProjectRelativeTime(project.updatedAt)}</Text>
        </View>
        <Text maxFontSizeMultiplier={1.6} numberOfLines={2} style={styles.projectTitle}>{project.title}</Text>
        <View style={styles.projectStatusRow}>
          <View accessibilityElementsHidden style={[styles.projectSignal, projectSignalStyle(project)]} />
          <Text numberOfLines={1} style={styles.projectStatus}>{studioProjectStatusLabel(project.status)}</Text>
          {active ? <Text style={styles.projectPercent}>{Math.max(0, Math.min(100, project.progressPercent))}%</Text> : null}
        </View>
      </View>
      <SymbolView name="chevron.right" tintColor={colors.text3} size={12} />
    </Pressable>
  );
});

function errorMessage(error: unknown): string {
  if (error instanceof BonfireApiError || error instanceof Error) return error.message;
  return 'Work could not be loaded.';
}

export function WorkHubScreen({ navigation, route }: Props) {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const isFocused = useIsFocused();
  const { width } = useWindowDimensions();
  const split = width >= WORK_HUB_SPLIT_WIDTH;
  const requestVersionRef = useRef(0);
  const handledRouteProjectRef = useRef('');
  const attemptedRouteProjectRef = useRef('');
  const currentRouteProjectRef = useRef('');
  const [projects, setProjects] = useState<StudioProject[]>([]);
  const [filter, setFilter] = useState<StudioProjectFilter>('all');
  const [selectedId, setSelectedId] = useState('');
  const [sheetVisible, setSheetVisible] = useState(false);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [nextBefore, setNextBefore] = useState('');
  const [hasMore, setHasMore] = useState(false);
  const [error, setError] = useState('');
  const [routeError, setRouteError] = useState('');
  const [actionError, setActionError] = useState('');
  const [busyAction, setBusyAction] = useState('');
  const [routeRetryVersion, setRouteRetryVersion] = useState(0);
  const requestedProjectId = route.name === 'WorkHome' ? String(route.params?.projectId ?? '').trim() : '';
  const requestedRootRunId = route.name === 'WorkHome' ? String(route.params?.rootRunId ?? '').trim() : '';
  currentRouteProjectRef.current = requestedProjectId
    ? `project:${requestedProjectId}`
    : requestedRootRunId
      ? `root:${requestedRootRunId}`
      : '';

  const selectedProject = useMemo(
    () => projects.find((project) => project.id === selectedId) ?? null,
    [projects, selectedId],
  );
  const rows = useMemo(() => studioProjectListRows(projects, filter), [filter, projects]);

  const load = useCallback(async (refresh = false, silent = false) => {
    if (!sessionToken) return;
    const version = ++requestVersionRef.current;
    setLoadingMore(false);
    if (!silent) {
      refresh ? setRefreshing(true) : setLoading(true);
      setError('');
    }
    try {
      const response = await api.studioProjects(sessionToken, { limit: 100 });
      if (version !== requestVersionRef.current) return;
      setProjects(response.projects ?? []);
      setNextBefore(String(response.nextBefore ?? ''));
      setHasMore(Boolean(response.hasMore && response.nextBefore));
    } catch (caught) {
      if (version === requestVersionRef.current && !silent) setError(errorMessage(caught));
    } finally {
      if (version === requestVersionRef.current) {
        if (!silent) {
          setLoading(false);
          setRefreshing(false);
        }
      }
    }
  }, [sessionToken]);

  const loadMore = useCallback(async () => {
    if (!sessionToken || !nextBefore || !hasMore || loadingMore) return;
    const version = ++requestVersionRef.current;
    setLoadingMore(true);
    try {
      const response = await api.studioProjects(sessionToken, { before: nextBefore, limit: 100 });
      if (version !== requestVersionRef.current) return;
      setProjects((current) => {
        const seen = new Set(current.map((project) => project.id));
        return [...current, ...(response.projects ?? []).filter((project) => !seen.has(project.id))];
      });
      setNextBefore(String(response.nextBefore ?? ''));
      setHasMore(Boolean(response.hasMore && response.nextBefore));
    } catch (caught) {
      if (version === requestVersionRef.current) setError(errorMessage(caught));
    } finally {
      if (version === requestVersionRef.current) setLoadingMore(false);
    }
  }, [hasMore, loadingMore, nextBefore, sessionToken]);

  useFocusEffect(useCallback(() => {
    void load();
  }, [load]));

  useEffect(() => {
    if (!isFocused || !['chat_thread', 'file', 'memory', 'action'].includes(office.event ?? '')) return;
    void load(false, true);
  }, [isFocused, load, office.event, office.version]);

  const hasLiveProjects = projects.some((project) => ['queued', 'running', 'needs_input', 'needs_attention'].includes(project.status));
  useEffect(() => {
    if (!isFocused || !sessionToken || !hasLiveProjects || loadingMore) return;
    const timer = setInterval(() => { void load(false, true); }, 6_000);
    return () => clearInterval(timer);
  }, [hasLiveProjects, isFocused, load, loadingMore, sessionToken]);

  useEffect(() => {
    if (!split) return;
    if (projects.length === 0) {
      if (selectedId) setSelectedId('');
      return;
    }
    if (!projects.some((project) => project.id === selectedId)) setSelectedId(projects[0].id);
  }, [projects, selectedId, split]);

  useEffect(() => {
    const requestKey = requestedProjectId ? `project:${requestedProjectId}` : requestedRootRunId ? `root:${requestedRootRunId}` : '';
    if (!requestKey) {
      handledRouteProjectRef.current = '';
      attemptedRouteProjectRef.current = '';
      return;
    }
    if (handledRouteProjectRef.current === requestKey || loading) return;
    const local = projects.find((project) => requestedProjectId
      ? project.id === requestedProjectId
      : project.rootRunId === requestedRootRunId);
    if (local) {
      handledRouteProjectRef.current = requestKey;
      attemptedRouteProjectRef.current = '';
      setRouteError('');
      setError('');
      setSelectedId(local.id);
      if (!split) setSheetVisible(true);
      if (route.name === 'WorkHome') navigation.setParams({ projectId: undefined, rootRunId: undefined });
      return;
    }
    const attemptKey = `${requestKey}:${routeRetryVersion}`;
    if (attemptedRouteProjectRef.current === attemptKey) return;
    attemptedRouteProjectRef.current = attemptKey;
    if (!sessionToken || !requestedProjectId) {
      setRouteError('That Work request is not in the projects loaded so far. Try again to refresh it.');
      return;
    }
    void api.studioProject(sessionToken, requestedProjectId)
      .then((response) => {
        if (currentRouteProjectRef.current !== requestKey) return;
        handledRouteProjectRef.current = requestKey;
        attemptedRouteProjectRef.current = '';
        setRouteError('');
        setError('');
        setProjects((current) => current.some((project) => project.id === response.project.id)
          ? current.map((project) => project.id === response.project.id ? response.project : project)
          : [response.project, ...current]);
        setSelectedId(response.project.id);
        if (!split) setSheetVisible(true);
        if (route.name === 'WorkHome') navigation.setParams({ projectId: undefined, rootRunId: undefined });
      })
      .catch((caught) => {
        if (currentRouteProjectRef.current !== requestKey) return;
        if (caught instanceof BonfireApiError && (caught.status === 403 || caught.status === 404)) {
          handledRouteProjectRef.current = requestKey;
          attemptedRouteProjectRef.current = '';
          setRouteError('That Work request is not available for this account.');
          if (route.name === 'WorkHome') navigation.setParams({ projectId: undefined, rootRunId: undefined });
          return;
        }
        setRouteError(errorMessage(caught));
      });
  }, [loading, navigation, projects, requestedProjectId, requestedRootRunId, route.name, routeRetryVersion, sessionToken, split]);

  const retryLoad = useCallback(() => {
    setError('');
    setRouteError('');
    if (requestedProjectId) {
      setRouteRetryVersion((version) => version + 1);
      return;
    }
    if (requestedRootRunId) {
      void load(true).finally(() => setRouteRetryVersion((version) => version + 1));
      return;
    }
    void load(true);
  }, [load, requestedProjectId, requestedRootRunId]);

  const openProject = useCallback((project: StudioProject) => {
    setActionError('');
    setSelectedId(project.id);
    if (!split) setSheetVisible(true);
    void Haptics.selectionAsync();
  }, [split]);

  const openResult = useCallback((project: StudioProject) => {
    const target = studioProjectOpenTarget(project);
    if (!target) {
      Alert.alert(
        project.kind === 'presentation' ? 'Presentation not ready' : 'Research not ready',
        project.kind === 'presentation' && project.result?.canEdit
          ? 'This draft is saved. Full slide editing is available on desktop.'
          : 'Scout has not attached an exact openable result yet.',
      );
      return;
    }
    if (!split) setSheetVisible(false);
    if (target.kind === 'deck') {
      navigation.navigate('DeckViewer', {
        artifactId: target.artifactId,
        artifactVersion: target.artifactVersion,
        artifactDigest: target.artifactDigest,
        title: target.title,
        desktopEditable: target.desktopEditable,
        previewOnly: !target.canPresent,
      });
      return;
    }
    navigation.navigate('OSWeb', {
      path: artifactStudioPath(
        target.artifactId,
        'document',
        artifactStudioIntent(target.canEdit),
        { version: target.artifactVersion, digest: target.artifactDigest },
      ),
      title: target.title,
    });
  }, [navigation, split]);

  const openSource = useCallback((project: StudioProject) => {
    const threadId = String(project.source?.threadId ?? '').trim();
    if (!threadId) return;
    if (!split) setSheetVisible(false);
    navigation.navigate('Thread', { threadId, title: project.title });
  }, [navigation, split]);

  const replaceProject = useCallback((next: StudioProject) => {
    setProjects((current) => current.map((project) => project.id === next.id ? next : project));
    setSelectedId(next.id);
  }, []);

  const surfaceActionError = useCallback((caught: unknown) => {
    const message = errorMessage(caught);
    setActionError(message);
    setError(message);
  }, []);

  const renameProject = useCallback((project: StudioProject) => {
    if (!sessionToken || busyAction) return;
    Alert.prompt(
      'Rename project',
      'Use a short name you will recognize later.',
      [
        { text: 'Cancel', style: 'cancel' },
        {
          text: 'Save',
          onPress: (value?: string) => {
            const title = String(value ?? '').trim();
            if (!title || title === project.title) return;
            setBusyAction('rename');
            setActionError('');
            void api.renameStudioProject(sessionToken, {
              id: project.id,
              title,
              expectedRevision: project.revision,
            }).then((response) => {
              replaceProject(response.project);
              void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
            }).catch(surfaceActionError).finally(() => setBusyAction(''));
          },
        },
      ],
      'plain-text',
      project.title,
    );
  }, [busyAction, replaceProject, sessionToken, surfaceActionError]);

  const resolveCheckpoint = useCallback((project: StudioProject, option: CheckpointOption) => {
    if (!sessionToken || !project.checkpoint || busyAction) return;
    const submit = (checkpointNote = '') => {
      setBusyAction(`checkpoint:${option.id}`);
      setActionError('');
      void api.artifactCheckpointAction(sessionToken, {
        id: project.rootArtifactId,
        checkpointId: project.checkpoint?.id ?? '',
        checkpointOptionId: option.id,
        ...(checkpointNote ? { checkpointNote } : {}),
      }).then(async () => {
        const response = await api.studioProject(sessionToken, project.id);
        replaceProject(response.project);
        void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
      }).catch(surfaceActionError).finally(() => setBusyAction(''));
    };
    if (option.action === 'revise') {
      Alert.prompt(
        'Changes for Scout',
        'Say what should improve and what must stay unchanged.',
        [
          { text: 'Cancel', style: 'cancel' },
          { text: 'Send changes', onPress: (note?: string) => { if (String(note ?? '').trim()) submit(String(note).trim()); } },
        ],
        'plain-text',
      );
      return;
    }
    Alert.alert('Confirm decision', option.label, [
      { text: 'Cancel', style: 'cancel' },
      { text: option.label, onPress: () => submit() },
    ]);
  }, [busyAction, replaceProject, sessionToken, surfaceActionError]);

  const continueResult = useCallback((project: StudioProject) => {
    if (!sessionToken || busyAction || project.result?.canContinue !== true || !project.result.artifactId) return;
    setBusyAction('continue');
    setActionError('');
    const reviewChanges = project.result.qualityState === 'edited_after_admission';
    const request = reviewChanges
      ? api.reviewEditedGoal(sessionToken, project.rootArtifactId, project.result.artifactId, project.result.version, project.result.digest)
      : api.resumeGoal(sessionToken, project.rootArtifactId);
    void request.then(async () => {
      const response = await api.studioProject(sessionToken, project.id);
      replaceProject(response.project);
      void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    }).catch(async (caught) => {
      surfaceActionError(caught);
      if (caught instanceof BonfireApiError && caught.status === 409) {
        try {
          const response = await api.studioProject(sessionToken, project.id);
          replaceProject(response.project);
        } catch {
          // Keep the conflict visible; the focused poll will retry hydration.
        }
      }
    }).finally(() => setBusyAction(''));
  }, [busyAction, replaceProject, sessionToken, surfaceActionError]);

  const header = useMemo(() => (
    <View style={styles.header}>
      <Text accessibilityRole="header" style={styles.title}>Work</Text>
      <Text style={styles.subtitle}>Presentations and research, organized from brief to final file.</Text>
      <View accessibilityRole="tablist" style={styles.filters}>
        {studioProjectFilters.map((item) => {
          const selected = filter === item.id;
          return (
            <Pressable
              key={item.id}
              accessibilityRole="tab"
              accessibilityState={{ selected }}
              onPress={() => setFilter(item.id)}
              style={({ pressed }) => [styles.filter, selected && styles.filterSelected, pressed && styles.filterPressed]}
            >
              <Text style={[styles.filterText, selected && styles.filterTextSelected]}>{item.label}</Text>
            </Pressable>
          );
        })}
      </View>
    </View>
  ), [filter]);

  const renderRow = useCallback(({ item }: { item: StudioProjectListRow }) => {
    if (item.type === 'section') {
      return <Text accessibilityRole="header" style={styles.sectionTitle}>{item.title.toUpperCase()}</Text>;
    }
    return <ProjectRow onPress={openProject} project={item.project} selected={split && selectedId === item.project.id} />;
  }, [openProject, selectedId, split]);

  const empty = !loading && rows.length === 0 ? (
    <View style={styles.empty}>
      <View style={styles.emptyIcon}>
        <SymbolView name="sparkles" tintColor={colors.emberText} size={20} />
      </View>
      <Text style={styles.emptyTitle}>{filter === 'all' ? 'No Studio projects yet' : `No ${filter === 'presentation' ? 'presentations' : 'research'} yet`}</Text>
      <Text style={styles.emptyBody}>Ask Scout for a presentation or research report. It will appear here as soon as the work starts.</Text>
    </View>
  ) : null;

  return (
    <SafeAreaView style={styles.safe} edges={['top', 'left', 'right']}>
      <View style={[styles.workspace, split && styles.workspaceSplit]}>
        <View style={[styles.listPane, split && styles.listPaneSplit]}>
          <FlashList
            data={rows}
            keyExtractor={(item) => item.id}
            renderItem={renderRow}
            ListHeaderComponent={header}
            ListEmptyComponent={loading ? (
              <View accessibilityRole="progressbar" accessibilityLabel="Loading Studio work" style={styles.loading}>
                <ActivityIndicator color={colors.emberText} />
                <Text style={styles.loadingText}>Loading work…</Text>
              </View>
            ) : empty}
            ListFooterComponent={loadingMore ? <ActivityIndicator color={colors.text3} style={styles.more} /> : null}
            contentContainerStyle={styles.listContent}
            keyboardDismissMode="on-drag"
            onEndReached={() => { void loadMore(); }}
            onEndReachedThreshold={0.35}
            onRefresh={() => { void load(true); }}
            refreshing={refreshing}
            showsVerticalScrollIndicator={false}
          />
          {routeError || error ? (
            <Pressable accessibilityRole="button" accessibilityLabel={`${routeError || error}. Try again`} onPress={retryLoad} style={styles.error}>
              <Text numberOfLines={2} style={styles.errorText}>{routeError || error}</Text>
              <Text style={styles.errorAction}>Try again</Text>
            </Pressable>
          ) : null}
        </View>
        {split ? (
          <View style={styles.detailPane}>
            <WorkProjectDetail
              actionError={actionError}
              busyAction={busyAction}
              project={selectedProject}
              onOpenResult={openResult}
              onOpenSource={openSource}
              onContinueResult={continueResult}
              onRename={renameProject}
              onResolveCheckpoint={resolveCheckpoint}
            />
          </View>
        ) : (
          <WorkProjectSheet
            actionError={actionError}
            busyAction={busyAction}
            project={selectedProject}
            visible={sheetVisible}
            onClose={() => { setSheetVisible(false); setActionError(''); }}
            onOpenResult={openResult}
            onOpenSource={openSource}
            onContinueResult={continueResult}
            onRename={renameProject}
            onResolveCheckpoint={resolveCheckpoint}
          />
        )}
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.bgApp },
  workspace: { flex: 1 },
  workspaceSplit: { flexDirection: 'row' },
  listPane: { flex: 1, minWidth: 0 },
  listPaneSplit: { width: 390, maxWidth: '44%', flex: 0, borderRightWidth: StyleSheet.hairlineWidth, borderRightColor: colors.line1 },
  detailPane: { flex: 1, minWidth: 0 },
  listContent: { paddingHorizontal: space[4], paddingBottom: space[6] },
  header: { gap: space[2], paddingTop: space[4], paddingBottom: space[5] },
  title: { ...type.title1, color: colors.text1 },
  subtitle: { ...type.body, maxWidth: 440, color: colors.text2 },
  filters: { minHeight: 48, alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: 3, marginTop: space[2], padding: 3, borderRadius: radius.full, backgroundColor: colors.surface2 },
  filter: { minHeight: 42, justifyContent: 'center', paddingHorizontal: space[4], borderRadius: radius.full },
  filterSelected: { backgroundColor: colors.accent },
  filterPressed: { opacity: 0.78, transform: [{ scale: 0.96 }] },
  filterText: { ...type.captionMedium, color: colors.text2 },
  filterTextSelected: { color: colors.onAccent },
  sectionTitle: { ...type.label, marginTop: space[3], marginBottom: space[2], paddingHorizontal: space[1], color: colors.text3, letterSpacing: 0.76 },
  projectRow: { minHeight: 88, flexDirection: 'row', alignItems: 'center', gap: space[3], marginBottom: space[2], padding: space[3], borderRadius: radius.xl, borderCurve: 'continuous', backgroundColor: colors.surface1, ...shadow[1] },
  projectRowSelected: { backgroundColor: colors.emberSoft },
  projectRowPressed: { opacity: 0.82, transform: [{ scale: 0.96 }] },
  projectIcon: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: radius.lg, backgroundColor: colors.emberSoft },
  projectCopy: { flex: 1, minWidth: 0 },
  projectMeta: { flexDirection: 'row', alignItems: 'center', gap: space[2] },
  projectKind: { ...type.label, flex: 1, color: colors.emberText },
  projectTime: { ...type.label, color: colors.text3, fontVariant: ['tabular-nums'] },
  projectTitle: { ...type.bodyMedium, marginTop: 2, color: colors.text1 },
  projectStatusRow: { minHeight: 18, flexDirection: 'row', alignItems: 'center', gap: 6, marginTop: 3 },
  projectSignal: { width: 7, height: 7, borderRadius: radius.full },
  projectSignalActive: { backgroundColor: colors.ember },
  projectSignalReady: { backgroundColor: colors.success },
  projectSignalAttention: { backgroundColor: colors.warn },
  projectSignalStopped: { backgroundColor: colors.text3 },
  projectStatus: { ...type.caption, flex: 1, color: colors.text2 },
  projectPercent: { ...type.captionMedium, color: colors.text2, fontVariant: ['tabular-nums'] },
  loading: { minHeight: 220, alignItems: 'center', justifyContent: 'center', gap: space[3] },
  loadingText: { ...type.caption, color: colors.text2 },
  more: { marginVertical: space[5] },
  empty: { minHeight: 280, alignItems: 'center', justifyContent: 'center', gap: space[2], paddingHorizontal: space[6] },
  emptyIcon: { width: 48, height: 48, alignItems: 'center', justifyContent: 'center', borderRadius: radius.xl, backgroundColor: colors.emberSoft },
  emptyTitle: { ...type.headline, color: colors.text1, textAlign: 'center' },
  emptyBody: { ...type.caption, maxWidth: 320, color: colors.text2, textAlign: 'center' },
  error: { minHeight: hitMin, flexDirection: 'row', alignItems: 'center', gap: space[3], margin: space[3], paddingHorizontal: space[4], borderRadius: radius.lg, backgroundColor: colors.dangerSoft },
  errorText: { ...type.caption, flex: 1, color: colors.text1 },
  errorAction: { ...type.captionMedium, color: colors.emberText },
});
