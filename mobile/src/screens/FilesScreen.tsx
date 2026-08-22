import React, { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ActionSheetIOS,
  ActivityIndicator,
  Alert,
  FlatList,
  Pressable,
  RefreshControl,
  StyleSheet,
  Text,
  View,
  type ListRenderItem,
} from 'react-native';
import { useFocusEffect } from '@react-navigation/native';
import type { NativeStackScreenProps } from '@react-navigation/native-stack';
import * as DocumentPicker from 'expo-document-picker';
import * as Haptics from 'expo-haptics';
import { SymbolView, type SFSymbol } from 'expo-symbols';
import { api } from '../api/client';
import {
  artifactStudioIntent,
  artifactStudioKind,
  artifactStudioPath,
} from '../artifacts/studioRoutes';
import { useAuth } from '../auth/AuthContext';
import { FilePreviewModal } from '../components/FilePreviewModal';
import { LongMessageSheet } from '../messaging/LongMessageSheet';
import { Screen } from '../components/Screen';
import { isInlinePreviewable, shareOrSaveRemoteFile } from '../files/fileActions';
import { useOfficeEvents } from '../realtime/OfficeEventsContext';
import { colors, hitMin, radius, shadow, space, type } from '../theme/tokens';
import type { RootStackParamList } from '../navigation/types';

type Props = NativeStackScreenProps<RootStackParamList, 'Files'>;

type FileRecord = {
  id: string;
  name: string;
  mime: string;
  size: number;
  uploaderName: string;
  createdAt: string;
  origin: string;
  brainStatus: string;
  folderId: string;
  downloadUrl: string;
  previewable: boolean;
  artifactId: string;
};

type FolderRecord = {
  id: string;
  name: string;
  count: number;
};

type FolderDestination = FolderRecord & { isRoot?: boolean };

const dateFormatter = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  year: 'numeric',
});

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

function parseFile(value: unknown): FileRecord | null {
  const row = asRecord(value);
  if (!row) return null;
  const id = asString(row.id);
  if (!id) return null;
  return {
    id,
    name: asString(row.name) || 'Untitled file',
    mime: asString(row.mime),
    size: typeof row.size === 'number' && Number.isFinite(row.size) ? row.size : 0,
    uploaderName: asString(row.uploaderName),
    createdAt: asString(row.createdAt),
    origin: asString(row.origin),
    brainStatus: asString(row.brainStatus),
    folderId: asString(row.folderId),
    downloadUrl: asString(row.downloadUrl),
    previewable: row.previewable === true,
    artifactId: asString(row.artifactId),
  };
}

function parseFolder(value: unknown): FolderRecord | null {
  const row = asRecord(value);
  if (!row) return null;
  const id = asString(row.id);
  const name = asString(row.name);
  if (!id || !name) return null;
  return {
    id,
    name,
    count: typeof row.count === 'number' && Number.isFinite(row.count) ? row.count : 0,
  };
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message.trim() ? error.message : fallback;
}

function formatBytes(bytes: number): string {
  if (bytes <= 0) return '';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  const megabytes = bytes / (1024 * 1024);
  return `${megabytes >= 10 ? Math.round(megabytes) : megabytes.toFixed(1)} MB`;
}

function formatDate(value: string): string {
  if (!value) return '';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '' : dateFormatter.format(date);
}

function fileIcon(file: FileRecord): SFSymbol {
  const mime = file.mime.toLowerCase();
  const extension = file.name.split('.').pop()?.toLowerCase() ?? '';
  if (mime.startsWith('image/')) return 'photo.fill';
  if (mime.startsWith('video/')) return 'play.rectangle.fill';
  if (mime.startsWith('audio/')) return 'waveform';
  if (mime === 'application/pdf' || extension === 'pdf') return 'doc.richtext.fill';
  if (['xls', 'xlsx', 'csv'].includes(extension)) return 'chart.bar.doc.horizontal.fill';
  if (['zip', 'gz', 'tar'].includes(extension)) return 'archivebox.fill';
  return 'doc.fill';
}

function fileKind(file: FileRecord): string {
  const extension = file.name.includes('.') ? file.name.split('.').pop()?.toUpperCase() : '';
  if (extension && extension.length <= 8) return extension;
  if (file.origin === 'deliverable') return 'DELIVERABLE';
  return 'FILE';
}

function notifySuccess() {
  void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success).catch(() => undefined);
}

type HeaderActionProps = {
  icon: SFSymbol;
  label: string;
  hint: string;
  busy: boolean;
  disabled: boolean;
  onPress: () => void;
};

const HeaderAction = memo(function HeaderAction({
  icon,
  label,
  hint,
  busy,
  disabled,
  onPress,
}: HeaderActionProps) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityHint={hint}
      accessibilityState={{ disabled }}
      disabled={disabled}
      hitSlop={4}
      onPress={onPress}
      style={({ pressed }) => [
        styles.headerAction,
        pressed && styles.pressed,
        disabled && styles.disabled,
      ]}
    >
      {busy ? (
        <ActivityIndicator size="small" color={colors.text1} />
      ) : (
        <SymbolView name={icon} tintColor={colors.text1} size={19} />
      )}
    </Pressable>
  );
});

type FolderChipProps = {
  folder: FolderDestination;
  selected: boolean;
  disabled: boolean;
  onOpen: (folder: FolderDestination) => void;
  onManage: (folder: FolderRecord) => void;
};

const FolderChip = memo(function FolderChip({
  folder,
  selected,
  disabled,
  onOpen,
  onManage,
}: FolderChipProps) {
  return (
    <View style={[styles.folderChip, selected && styles.folderChipSelected]}>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={`${folder.name}, ${folder.count} ${folder.count === 1 ? 'file' : 'files'}`}
        accessibilityHint="Shows the files in this location"
        accessibilityState={{ selected, disabled }}
        disabled={disabled}
        onPress={() => onOpen(folder)}
        style={({ pressed }) => [styles.folderOpen, pressed && styles.pressed]}
      >
        <SymbolView
          name={folder.isRoot ? 'tray.full.fill' : 'folder.fill'}
          tintColor={selected ? colors.onAccent : colors.text2}
          size={19}
        />
        <View style={styles.folderCopy}>
          <Text
            style={[styles.folderName, selected && styles.folderNameSelected]}
            numberOfLines={1}
          >
            {folder.name}
          </Text>
          <Text style={[styles.folderCount, selected && styles.folderCountSelected]}>
            {folder.count}
          </Text>
        </View>
      </Pressable>
      {!folder.isRoot ? (
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={`Manage ${folder.name}`}
          accessibilityHint="Rename or delete this folder"
          accessibilityState={{ disabled }}
          disabled={disabled}
          hitSlop={4}
          onPress={() => onManage(folder)}
          style={({ pressed }) => [styles.folderMenu, pressed && styles.pressed]}
        >
          <SymbolView
            name="ellipsis"
            tintColor={selected ? colors.onAccent : colors.text2}
            size={17}
          />
        </Pressable>
      ) : null}
    </View>
  );
});

type FileRowProps = {
  file: FileRecord;
  moving: boolean;
  sharing: boolean;
  disabled: boolean;
  onOpen: (file: FileRecord) => void;
  onManage: (file: FileRecord) => void;
};

const FileRow = memo(function FileRow({
  file,
  moving,
  sharing,
  disabled,
  onOpen,
  onManage,
}: FileRowProps) {
  const metadata = [
    formatBytes(file.size),
    file.uploaderName ? `by ${file.uploaderName}` : '',
    formatDate(file.createdAt),
  ].filter(Boolean);
  const brainLabel =
    file.brainStatus === 'ingested'
      ? 'In company memory'
      : file.brainStatus === 'thread'
        ? 'In thread context'
        : 'Stored safely';

  return (
    <View style={[styles.fileRow, shadow[1]]}>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={`Open ${file.name}`}
        accessibilityHint={file.downloadUrl ? 'Opens a preview or the system file actions' : 'File data is unavailable'}
        accessibilityState={{ disabled: disabled || !file.downloadUrl }}
        disabled={disabled || !file.downloadUrl}
        onPress={() => onOpen(file)}
        style={({ pressed }) => [styles.fileOpen, pressed && styles.fileOpenPressed]}
      >
        <View style={styles.fileIcon}>
          <SymbolView name={fileIcon(file)} tintColor={colors.text1} size={23} />
        </View>
        <View style={styles.fileCopy}>
          <Text style={styles.fileName} numberOfLines={2}>
            {file.name}
          </Text>
          {metadata.length > 0 ? (
            <Text style={styles.fileMeta} numberOfLines={1}>
              {metadata.join(' · ')}
            </Text>
          ) : null}
          <View style={styles.badges}>
            <View style={styles.kindBadge}>
              <Text style={styles.kindBadgeText}>{fileKind(file)}</Text>
            </View>
            <View style={file.brainStatus === 'ingested' ? styles.brainBadge : styles.storedBadge}>
              <SymbolView
                name={file.brainStatus === 'ingested' ? 'brain.head.profile.fill' : 'externaldrive.fill'}
                tintColor={file.brainStatus === 'ingested' ? colors.live : colors.text2}
                size={12}
              />
              <Text
                style={file.brainStatus === 'ingested' ? styles.brainBadgeText : styles.storedBadgeText}
                numberOfLines={1}
              >
                {brainLabel}
              </Text>
            </View>
          </View>
        </View>
      </Pressable>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={`More actions for ${file.name}`}
        accessibilityHint="Rename, move, or share this file"
        accessibilityState={{ disabled }}
        disabled={disabled}
        hitSlop={4}
        onPress={() => onManage(file)}
        style={({ pressed }) => [styles.fileMenu, pressed && styles.pressed]}
      >
        {moving || sharing ? <ActivityIndicator size="small" color={colors.text2} /> : (
          <View style={styles.verticalEllipsis}>
            <SymbolView name="ellipsis" tintColor={colors.text2} size={19} />
          </View>
        )}
      </Pressable>
    </View>
  );
});

export function FilesScreen({ navigation, route }: Props) {
  const { sessionToken } = useAuth();
  const office = useOfficeEvents();
  const requestVersion = useRef(0);
  const [files, setFiles] = useState<FileRecord[]>([]);
  const [folders, setFolders] = useState<FolderRecord[]>([]);
  const [activeFolderId, setActiveFolderId] = useState('');
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [preview, setPreview] = useState<FileRecord | null>(null);
  const [artifactPreview, setArtifactPreview] = useState<{ title: string; text: string } | null>(null);

  const load = useCallback(
    async (refresh = false) => {
      if (!sessionToken) return;
      const version = ++requestVersion.current;
      refresh ? setRefreshing(true) : setLoading(true);
      setLoadError(null);
      try {
        const response = await api.files(sessionToken);
        if (version !== requestVersion.current) return;
        setFiles(response.files.map(parseFile).filter((file): file is FileRecord => Boolean(file)));
        const nextFolders = response.folders
          .map(parseFolder)
          .filter((folder): folder is FolderRecord => Boolean(folder));
        setFolders(nextFolders);
        setActiveFolderId((current) =>
          current && !nextFolders.some((folder) => folder.id === current) ? '' : current,
        );
      } catch (error) {
        if (version === requestVersion.current) {
          setLoadError(errorMessage(error, 'Could not load files.'));
        }
      } finally {
        if (version === requestVersion.current) {
          setLoading(false);
          setRefreshing(false);
        }
      }
    },
    [sessionToken],
  );

  useFocusEffect(
    useCallback(() => {
      void load();
    }, [load]),
  );

  useEffect(() => {
    if (office.event === 'file') void load(true);
  }, [load, office.event, office.version]);

  const visibleFiles = useMemo(
    () => files.filter((file) => file.folderId === activeFolderId),
    [activeFolderId, files],
  );

  const destinations = useMemo<FolderDestination[]>(() => {
    const rootCount = files.reduce((count, file) => count + (file.folderId ? 0 : 1), 0);
    return [{ id: '', name: 'Root', count: rootCount, isRoot: true }, ...folders];
  }, [files, folders]);

  const activeFolder = destinations.find((folder) => folder.id === activeFolderId) ?? destinations[0];
  const isBusy = busy !== null;

  const runMutation = useCallback(
    async (key: string, action: () => Promise<void>) => {
      if (busy) return;
      setBusy(key);
      setActionError(null);
      try {
        await action();
        notifySuccess();
        await load(true);
      } catch (error) {
        setActionError(errorMessage(error, 'That change could not be saved.'));
      } finally {
        setBusy(null);
      }
    },
    [busy, load],
  );

  const createFolder = useCallback(
    (rawName: string) => {
      const name = rawName.trim();
      if (!sessionToken || !name) return;
      void runMutation('new-folder', async () => {
        const result = await api.createFileFolder(sessionToken, name);
        setActiveFolderId(result.folder.id);
      });
    },
    [runMutation, sessionToken],
  );

  const promptForFolder = useCallback(() => {
    Alert.prompt(
      'New folder',
      'Give this collection a short, memorable name.',
      [
        { text: 'Cancel', style: 'cancel' },
        { text: 'Create', onPress: (name: string | undefined) => createFolder(name ?? '') },
      ],
      'plain-text',
      '',
    );
  }, [createFolder]);

  const renameFolder = useCallback(
    (folder: FolderRecord, rawName: string) => {
      const name = rawName.trim();
      if (!sessionToken || !name || name === folder.name) return;
      void runMutation(`folder-${folder.id}`, async () => {
        await api.renameFileFolder(sessionToken, folder.id, name);
      });
    },
    [runMutation, sessionToken],
  );

  const confirmDeleteFolder = useCallback(
    (folder: FolderRecord) => {
      if (!sessionToken) return;
      Alert.alert(
        `Delete “${folder.name}”?`,
        'The folder will be removed. Its files will return to Root.',
        [
          { text: 'Cancel', style: 'cancel' },
          {
            text: 'Delete folder',
            style: 'destructive',
            onPress: () => {
              void runMutation(`folder-${folder.id}`, async () => {
                await api.deleteFileFolder(sessionToken, folder.id);
                setActiveFolderId((current) => (current === folder.id ? '' : current));
              });
            },
          },
        ],
      );
    },
    [runMutation, sessionToken],
  );

  const showFolderMenu = useCallback(
    (folder: FolderRecord) => {
      ActionSheetIOS.showActionSheetWithOptions(
        {
          title: folder.name,
          options: ['Rename', 'Delete folder', 'Cancel'],
          cancelButtonIndex: 2,
          destructiveButtonIndex: 1,
        },
        (index) => {
          if (index === 0) {
            Alert.prompt(
              'Rename folder',
              undefined,
              [
                { text: 'Cancel', style: 'cancel' },
                {
                  text: 'Save',
                  onPress: (name: string | undefined) => renameFolder(folder, name ?? ''),
                },
              ],
              'plain-text',
              folder.name,
            );
          } else if (index === 1) {
            confirmDeleteFolder(folder);
          }
        },
      );
    },
    [confirmDeleteFolder, renameFolder],
  );

  const uploadDocument = useCallback(async () => {
    if (!sessionToken || busy) return;
    setActionError(null);
    try {
      const result = await DocumentPicker.getDocumentAsync({
        copyToCacheDirectory: true,
        multiple: false,
      });
      if (result.canceled) return;
      const asset = result.assets[0];
      if (!asset) return;
      await runMutation('upload', async () => {
        const uploaded = await api.uploadFile(sessionToken, {
          uri: asset.uri,
          name: asset.name || 'file',
          mime: asset.mimeType || 'application/octet-stream',
        });
        const fileId = asString(uploaded.file.id);
        if (activeFolderId && fileId) {
          await api.moveFile(sessionToken, fileId, activeFolderId);
        }
      });
    } catch (error) {
      setActionError(errorMessage(error, 'Could not open the document picker.'));
    }
  }, [activeFolderId, busy, runMutation, sessionToken]);

  const showMoveMenu = useCallback(
    (file: FileRecord) => {
      if (!sessionToken || busy) return;
      const choices = destinations.map((folder) =>
        folder.id === file.folderId ? `✓ ${folder.name}` : folder.name,
      );
      const cancelIndex = choices.length;
      ActionSheetIOS.showActionSheetWithOptions(
        {
          title: `Move “${file.name}”`,
          message: 'Choose a destination',
          options: [...choices, 'Cancel'],
          cancelButtonIndex: cancelIndex,
        },
        (index) => {
          if (index === cancelIndex) return;
          const destination = destinations[index];
          if (!destination || destination.id === file.folderId) return;
          void runMutation(`file-${file.id}`, async () => {
            await api.moveFile(sessionToken, file.id, destination.id);
          });
        },
      );
    },
    [busy, destinations, runMutation, sessionToken],
  );

  const openFile = useCallback(
    (file: FileRecord) => {
      if (!sessionToken || busy) return;
      if (file.artifactId) {
        void (async () => {
          setBusy(`open-${file.id}`);
          setActionError(null);
          try {
            const response = await api.artifact(sessionToken, file.artifactId);
            const artifact = response.artifacts[0];
            const title = String(artifact?.metadata?.title ?? file.name).trim() || file.name;
            const studioKind = artifactStudioKind(
              artifact?.metadata?.type ?? artifact?.metadata?.artifactType,
            );
            if (studioKind) {
              const access = await api.artifactStudioAccess(
                sessionToken,
                file.artifactId,
                studioKind,
              );
              navigation.navigate('OSWeb', {
                path: artifactStudioPath(
                  file.artifactId,
                  studioKind,
                  artifactStudioIntent(access.canWrite),
                ),
                title,
              });
              return;
            }
            const text = String(artifact?.text ?? '').trim();
            if (!text) throw new Error('The completed deliverable is not available yet.');
            setArtifactPreview({
              title,
              text,
            });
          } catch (error) {
            setActionError(errorMessage(error, 'Could not open the deliverable.'));
          } finally {
            setBusy(null);
          }
        })();
        return;
      }
      if (!file.downloadUrl) {
        setActionError('This item does not include downloadable file data yet.');
        return;
      }
      if (file.previewable || isInlinePreviewable(file)) {
        setPreview(file);
        return;
      }
      void (async () => {
        setBusy(`share-${file.id}`);
        setActionError(null);
        try {
          await shareOrSaveRemoteFile(sessionToken, file);
        } catch (error) {
          setActionError(errorMessage(error, 'Could not open the file.'));
        } finally {
          setBusy(null);
        }
      })();
    },
    [busy, navigation, sessionToken],
  );

  useEffect(() => {
    const fileId = String(route.params?.fileId ?? '').trim();
    if (!fileId || loading || busy) return;
    const file = files.find((candidate) => candidate.id === fileId);
    navigation.setParams({ fileId: undefined });
    if (!file) {
      setActionError('That saved Drive file is unavailable.');
      return;
    }
    setActiveFolderId(file.folderId);
    openFile(file);
  }, [busy, files, loading, navigation, openFile, route.params?.fileId]);

  const shareFile = useCallback(
    (file: FileRecord) => {
      if (!sessionToken || busy || !file.downloadUrl) return;
      void (async () => {
        setBusy(`share-${file.id}`);
        setActionError(null);
        try {
          await shareOrSaveRemoteFile(sessionToken, file);
        } catch (error) {
          setActionError(errorMessage(error, 'Could not share the file.'));
        } finally {
          setBusy(null);
        }
      })();
    },
    [busy, sessionToken],
  );

  const renameFile = useCallback((file: FileRecord, rawName: string) => {
    const name = rawName.trim();
    if (!sessionToken || !name || name === file.name) return;
    void runMutation(`rename-${file.id}`, async () => {
      await api.renameFile(sessionToken, file.id, name);
    });
  }, [runMutation, sessionToken]);

  const showFileMenu = useCallback((file: FileRecord) => {
    if (busy) return;
    const canShare = Boolean(file.downloadUrl);
    const options = ['Rename', 'Move', ...(canShare ? ['Share or save'] : []), 'Cancel'];
    const cancelButtonIndex = options.length - 1;
    ActionSheetIOS.showActionSheetWithOptions(
      {
        title: file.name,
        options,
        cancelButtonIndex,
      },
      (index) => {
        if (index === 0) {
          Alert.prompt(
            'Rename file',
            'Choose the name shown in Drive.',
            [
              { text: 'Cancel', style: 'cancel' },
              { text: 'Save', onPress: (name?: string) => renameFile(file, name ?? '') },
            ],
            'plain-text',
            file.name,
          );
        } else if (index === 1) {
          showMoveMenu(file);
        } else if (canShare && index === 2) {
          shareFile(file);
        }
      },
    );
  }, [busy, renameFile, shareFile, showMoveMenu]);

  const openFolder = useCallback((folder: FolderDestination) => {
    setActiveFolderId(folder.id);
    setActionError(null);
  }, []);

  const renderFolder: ListRenderItem<FolderDestination> = useCallback(
    ({ item }) => (
      <FolderChip
        folder={item}
        selected={item.id === activeFolderId}
        disabled={isBusy}
        onOpen={openFolder}
        onManage={showFolderMenu}
      />
    ),
    [activeFolderId, isBusy, openFolder, showFolderMenu],
  );

  const renderFile: ListRenderItem<FileRecord> = useCallback(
    ({ item }) => (
      <FileRow
        file={item}
        moving={busy === `file-${item.id}`}
        sharing={busy === `share-${item.id}`}
        disabled={isBusy}
        onOpen={openFile}
        onManage={showFileMenu}
      />
    ),
    [busy, isBusy, openFile, showFileMenu],
  );

  const listHeader = (
    <View>
      {actionError ? (
        <View style={styles.actionError} accessibilityRole="alert">
          <SymbolView name="exclamationmark.circle.fill" tintColor={colors.danger} size={18} />
          <Text style={styles.actionErrorText}>{actionError}</Text>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Dismiss error"
            hitSlop={8}
            onPress={() => setActionError(null)}
            style={({ pressed }) => [styles.dismiss, pressed && styles.pressed]}
          >
            <SymbolView name="xmark" tintColor={colors.text2} size={14} />
          </Pressable>
        </View>
      ) : null}

      <View style={styles.sectionHeading}>
        <Text style={styles.sectionLabel}>LOCATIONS</Text>
        <Text style={styles.sectionMeta}>{files.length} total</Text>
      </View>
      <FlatList
        horizontal
        data={destinations}
        keyExtractor={(item) => item.id || 'root'}
        renderItem={renderFolder}
        contentContainerStyle={styles.folderList}
        showsHorizontalScrollIndicator={false}
        keyboardShouldPersistTaps="handled"
      />

      <View style={styles.currentLocation}>
        <View>
          <Text style={styles.locationTitle}>{activeFolder.name}</Text>
          <Text style={styles.locationSubtitle}>
            {visibleFiles.length} {visibleFiles.length === 1 ? 'file' : 'files'}
          </Text>
        </View>
        {activeFolderId ? (
          <View style={styles.folderBadge}>
            <SymbolView name="folder.fill" tintColor={colors.text2} size={13} />
            <Text style={styles.folderBadgeText}>Folder</Text>
          </View>
        ) : null}
      </View>
    </View>
  );

  return (
    <Screen
      title="Files"
      subtitle="Uploads and finished work, organized natively"
      loading={loading}
      error={loadError}
      onRetry={() => void load()}
      scroll={false}
      right={
        <View style={styles.headerActions}>
          <HeaderAction
            icon="doc.badge.plus"
            label="Upload document"
            hint={activeFolderId ? `Uploads into ${activeFolder.name}` : 'Uploads into Root'}
            busy={busy === 'upload'}
            disabled={isBusy}
            onPress={() => void uploadDocument()}
          />
          <HeaderAction
            icon="folder.badge.plus"
            label="Create folder"
            hint="Creates a new file folder"
            busy={busy === 'new-folder'}
            disabled={isBusy}
            onPress={promptForFolder}
          />
        </View>
      }
    >
      <FlatList
        data={visibleFiles}
        keyExtractor={(item) => item.id}
        renderItem={renderFile}
        ListHeaderComponent={listHeader}
        ListEmptyComponent={
          <View style={styles.empty}>
            <View style={styles.emptyIcon}>
              <SymbolView name="folder" tintColor={colors.text3} size={28} />
            </View>
            <Text style={styles.emptyTitle}>Nothing here yet</Text>
            <Text style={styles.emptyCopy}>
              Upload a document or move a file into this location.
            </Text>
          </View>
        }
        contentContainerStyle={styles.listContent}
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            onRefresh={() => void load(true)}
            tintColor={colors.accent}
          />
        }
        keyboardShouldPersistTaps="handled"
        showsVerticalScrollIndicator={false}
      />
      <FilePreviewModal
        file={preview}
        sessionToken={sessionToken ?? ''}
        onClose={() => setPreview(null)}
      />
      <LongMessageSheet
        visible={Boolean(artifactPreview)}
        text={artifactPreview?.text ?? ''}
        authorName={artifactPreview?.title ?? 'Deliverable'}
        scout
        onClose={() => setArtifactPreview(null)}
      />
    </Screen>
  );
}

const styles = StyleSheet.create({
  headerActions: {
    flexDirection: 'row',
    gap: space[2],
  },
  headerAction: {
    width: hitMin,
    height: hitMin,
    borderRadius: radius.md,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
  },
  pressed: {
    opacity: 0.7,
    transform: [{ scale: 0.96 }],
  },
  disabled: {
    opacity: 0.55,
  },
  listContent: {
    paddingBottom: space[10],
  },
  actionError: {
    minHeight: hitMin,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
    backgroundColor: colors.dangerSoft,
    borderRadius: radius.md,
    paddingLeft: space[3],
    marginBottom: space[4],
  },
  actionErrorText: {
    flex: 1,
    ...type.caption,
    color: colors.danger,
    paddingVertical: space[2],
  },
  dismiss: {
    width: hitMin,
    minHeight: hitMin,
    alignItems: 'center',
    justifyContent: 'center',
  },
  sectionHeading: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: space[2],
  },
  sectionLabel: {
    ...type.label,
    color: colors.text3,
  },
  sectionMeta: {
    ...type.caption,
    color: colors.text3,
  },
  folderList: {
    gap: space[2],
    paddingBottom: space[5],
  },
  folderChip: {
    minHeight: 56,
    minWidth: 116,
    maxWidth: 208,
    flexDirection: 'row',
    alignItems: 'stretch',
    borderRadius: radius.lg,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    overflow: 'hidden',
  },
  folderChipSelected: {
    backgroundColor: colors.accent,
    borderColor: colors.accent,
  },
  folderOpen: {
    minHeight: 56,
    flex: 1,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
    paddingLeft: space[3],
    paddingVertical: space[2],
  },
  folderCopy: {
    flex: 1,
  },
  folderName: {
    ...type.captionMedium,
    color: colors.text1,
  },
  folderNameSelected: {
    color: colors.onAccent,
  },
  folderCount: {
    ...type.caption,
    color: colors.text3,
  },
  folderCountSelected: {
    color: colors.onAccent,
    opacity: 0.68,
  },
  folderMenu: {
    width: hitMin,
    minHeight: 56,
    alignItems: 'center',
    justifyContent: 'center',
  },
  currentLocation: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: space[3],
  },
  locationTitle: {
    ...type.title2,
    color: colors.text1,
  },
  locationSubtitle: {
    marginTop: 2,
    ...type.caption,
    color: colors.text2,
  },
  folderBadge: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 5,
    borderRadius: radius.full,
    backgroundColor: colors.surface3,
    paddingHorizontal: space[3],
    paddingVertical: 6,
  },
  folderBadgeText: {
    ...type.label,
    color: colors.text2,
    textTransform: 'uppercase',
  },
  fileRow: {
    minHeight: 92,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 0,
    borderRadius: radius.lg,
    backgroundColor: colors.surface1,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: colors.line1,
    paddingLeft: space[3],
    marginBottom: space[3],
  },
  fileOpen: {
    minHeight: 92,
    flex: 1,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
  },
  fileOpenPressed: {
    opacity: 0.72,
  },
  fileIcon: {
    width: 46,
    height: 54,
    borderRadius: radius.md,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.surface3,
  },
  fileCopy: {
    flex: 1,
    paddingVertical: space[3],
  },
  fileName: {
    ...type.bodyMedium,
    color: colors.text1,
  },
  fileMeta: {
    marginTop: 2,
    ...type.caption,
    color: colors.text2,
  },
  badges: {
    flexDirection: 'row',
    alignItems: 'center',
    flexWrap: 'wrap',
    gap: 6,
    marginTop: space[2],
  },
  kindBadge: {
    borderRadius: radius.full,
    backgroundColor: colors.surface3,
    paddingHorizontal: 7,
    paddingVertical: 3,
  },
  kindBadgeText: {
    ...type.label,
    color: colors.text2,
  },
  brainBadge: {
    maxWidth: 146,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 4,
    borderRadius: radius.full,
    backgroundColor: colors.liveSoft,
    paddingHorizontal: 7,
    paddingVertical: 3,
  },
  brainBadgeText: {
    flexShrink: 1,
    ...type.label,
    color: colors.live,
  },
  storedBadge: {
    maxWidth: 146,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 4,
    borderRadius: radius.full,
    backgroundColor: colors.surface3,
    paddingHorizontal: 7,
    paddingVertical: 3,
  },
  storedBadgeText: {
    flexShrink: 1,
    ...type.label,
    color: colors.text2,
  },
  fileMenu: {
    width: hitMin,
    minHeight: 92,
    alignItems: 'center',
    justifyContent: 'center',
  },
  verticalEllipsis: { transform: [{ rotate: '90deg' }] },
  empty: {
    alignItems: 'center',
    paddingHorizontal: space[8],
    paddingTop: space[8],
  },
  emptyIcon: {
    width: 58,
    height: 58,
    borderRadius: radius.xl,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.surface3,
    marginBottom: space[3],
  },
  emptyTitle: {
    ...type.headline,
    color: colors.text1,
  },
  emptyCopy: {
    marginTop: 4,
    ...type.bodySm,
    color: colors.text2,
    textAlign: 'center',
  },
});
