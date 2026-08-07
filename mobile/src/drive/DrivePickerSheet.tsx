import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  ActivityIndicator,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { SymbolView } from 'expo-symbols';

import { api } from '../api/client';
import type { DriveFileRecord, DriveFolderRecord } from '../api/types';
import { colors, hitMin, radius, shadow, space, type } from '../theme/tokens';
import {
  driveFilesForLocation,
  driveFolderChildren,
  parseDriveFiles,
  parseDriveFolders,
} from './driveModels';

type BrowserData = { files: DriveFileRecord[]; folders: DriveFolderRecord[] };

function useDriveBrowser(visible: boolean, sessionToken: string) {
  const [data, setData] = useState<BrowserData>({ files: [], folders: [] });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    if (!visible || !sessionToken) return;
    setLoading(true);
    setError('');
    try {
      const response = await api.files(sessionToken);
      setData({
        files: parseDriveFiles(response.files),
        folders: parseDriveFolders(response.folders),
      });
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Drive could not be opened.');
    } finally {
      setLoading(false);
    }
  }, [sessionToken, visible]);

  useEffect(() => { void load(); }, [load]);
  return { ...data, loading, error, reload: load };
}

function locationName(folders: readonly DriveFolderRecord[], folderId: string): string {
  return folders.find((folder) => folder.id === folderId)?.name ?? 'Drive';
}

function parentFolderId(folders: readonly DriveFolderRecord[], folderId: string): string {
  return String(folders.find((folder) => folder.id === folderId)?.parentId ?? '');
}

type SheetFrameProps = {
  title: string;
  subtitle: string;
  onClose: () => void;
  children: React.ReactNode;
};

function SheetFrame({ title, subtitle, onClose, children }: SheetFrameProps) {
  return (
    <SafeAreaView style={styles.safe} edges={['left', 'right', 'bottom']}>
      <View style={styles.handle} />
      <View style={styles.header}>
        <View style={styles.headerCopy}>
          <Text style={styles.eyebrow}>BONFIRE DRIVE</Text>
          <Text accessibilityRole="header" numberOfLines={1} style={styles.title}>{title}</Text>
          <Text numberOfLines={1} style={styles.subtitle}>{subtitle}</Text>
        </View>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Close Drive"
          onPress={onClose}
          style={({ pressed }) => [styles.close, pressed && styles.pressed]}
        >
          <SymbolView name="xmark" tintColor={colors.text2} size={15} />
        </Pressable>
      </View>
      {children}
    </SafeAreaView>
  );
}

type DriveFilePickerSheetProps = {
  visible: boolean;
  sessionToken: string;
  initialQuery?: string;
  selectionMode?: 'single' | 'multiple';
  maxSelection?: number;
  onClose: () => void;
  onChoose: (files: DriveFileRecord[]) => void;
};

export function DriveFilePickerSheet({
  visible,
  sessionToken,
  initialQuery = '',
  selectionMode = 'multiple',
  maxSelection = 6,
  onClose,
  onChoose,
}: DriveFilePickerSheetProps) {
  const { files, folders, loading, error, reload } = useDriveBrowser(visible, sessionToken);
  const [folderId, setFolderId] = useState('');
  const [query, setQuery] = useState(initialQuery);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);

  useEffect(() => {
    if (!visible) return;
    setFolderId('');
    setQuery(initialQuery);
    setSelectedIds([]);
  }, [initialQuery, visible]);

  const visibleFolders = useMemo(
    () => query.trim() ? [] : driveFolderChildren(folders, folderId),
    [folderId, folders, query],
  );
  const visibleFiles = useMemo(() => {
    if (query.trim()) {
      const needle = query.trim().toLocaleLowerCase();
      return files.filter((file) => file.name.toLocaleLowerCase().includes(needle));
    }
    return driveFilesForLocation(files, folderId);
  }, [files, folderId, query]);
  const selected = useMemo(
    () => selectedIds.flatMap((id) => files.find((file) => file.id === id) ?? []),
    [files, selectedIds],
  );

  const toggle = (file: DriveFileRecord) => {
    setSelectedIds((current) => {
      if (maxSelection <= 0) return current;
      if (current.includes(file.id)) return current.filter((id) => id !== file.id);
      if (selectionMode === 'single') return [file.id];
      return [...current, file.id].slice(0, maxSelection);
    });
  };

  return (
    <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" onRequestClose={onClose}>
      <SheetFrame title="Browse Drive" subtitle={locationName(folders, folderId)} onClose={onClose}>
        <View style={styles.searchFrame}>
          <SymbolView name="magnifyingglass" tintColor={colors.text3} size={16} />
          <TextInput
            accessibilityLabel="Search authorized Drive files"
            autoFocus={Boolean(initialQuery)}
            onChangeText={setQuery}
            placeholder="Search files"
            placeholderTextColor={colors.text3}
            selectionColor={colors.info}
            style={styles.searchInput}
            value={query}
          />
        </View>
        <View style={styles.locationBar}>
          {folderId ? (
            <Pressable
              accessibilityRole="button"
              accessibilityLabel="Go to parent folder"
              onPress={() => setFolderId(parentFolderId(folders, folderId))}
              style={({ pressed }) => [styles.back, pressed && styles.pressed]}
            >
              <SymbolView name="chevron.left" tintColor={colors.text2} size={14} />
              <Text style={styles.backText}>Back</Text>
            </Pressable>
          ) : <Text style={styles.locationHint}>Only files you can access appear here.</Text>}
        </View>
        <ScrollView contentContainerStyle={styles.list} keyboardShouldPersistTaps="handled">
          {loading ? <ActivityIndicator accessibilityLabel="Loading Drive" color={colors.emberText} /> : null}
          {error ? (
            <Pressable accessibilityRole="button" accessibilityLabel="Retry Drive" onPress={() => void reload()} style={({ pressed }) => [styles.retry, pressed && styles.pressed]}>
              <Text style={styles.error}>{error}</Text>
              <Text style={styles.retryText}>Try again</Text>
            </Pressable>
          ) : null}
          {visibleFolders.map((folder) => (
            <Pressable
              key={folder.id}
              accessibilityRole="button"
              accessibilityLabel={`Open ${folder.name}`}
              onPress={() => setFolderId(folder.id)}
              style={({ pressed }) => [styles.row, pressed && styles.pressed]}
            >
              <View style={styles.rowIcon}><SymbolView name="folder.fill" tintColor={colors.emberText} size={19} /></View>
              <Text numberOfLines={1} style={styles.rowName}>{folder.name}</Text>
              <SymbolView name="chevron.right" tintColor={colors.text3} size={13} />
            </Pressable>
          ))}
          {visibleFiles.map((file) => {
            const checked = selectedIds.includes(file.id);
            return (
              <Pressable
                key={file.id}
                accessibilityRole="checkbox"
                accessibilityLabel={`Select ${file.name}`}
                accessibilityState={{ checked, disabled: maxSelection <= 0 }}
                disabled={maxSelection <= 0}
                onPress={() => toggle(file)}
                style={({ pressed }) => [styles.row, checked && styles.rowSelected, pressed && styles.pressed]}
              >
                <View style={[styles.rowIcon, checked && styles.rowIconSelected]}>
                  <SymbolView name={checked ? 'checkmark' : 'doc.fill'} tintColor={checked ? colors.onAccent : colors.text2} size={17} />
                </View>
                <View style={styles.rowCopy}>
                  <Text numberOfLines={1} style={styles.rowName}>{file.name}</Text>
                  <Text numberOfLines={1} style={styles.rowMeta}>{locationName(folders, String(file.folderId ?? ''))}</Text>
                </View>
              </Pressable>
            );
          })}
          {!loading && !error && visibleFolders.length === 0 && visibleFiles.length === 0 ? (
            <View style={styles.empty}>
              <SymbolView name="doc.text.magnifyingglass" tintColor={colors.text3} size={26} />
              <Text style={styles.emptyTitle}>{query.trim() ? 'No matching files' : 'Nothing in this folder'}</Text>
            </View>
          ) : null}
        </ScrollView>
        <View style={styles.footer}>
          <Text style={styles.selectionCount}>{maxSelection <= 0 ? 'Attachment limit reached' : `${selected.length} selected`}</Text>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={selected.length === 1 ? 'Attach selected Drive file' : `Attach ${selected.length} selected Drive files`}
            accessibilityState={{ disabled: selected.length === 0 }}
            disabled={selected.length === 0}
            onPress={() => onChoose(selected)}
            style={({ pressed }) => [styles.primary, pressed && styles.pressed, selected.length === 0 && styles.disabled]}
          >
            <Text style={styles.primaryText}>Attach</Text>
          </Pressable>
        </View>
      </SheetFrame>
    </Modal>
  );
}

type ArtifactSaveSheetProps = {
  visible: boolean;
  sessionToken: string;
  defaultName: string;
  saving: boolean;
  error?: string;
  onClose: () => void;
  onSave: (fileName: string, folderId: string) => void;
};

export function ArtifactSaveSheet({ visible, sessionToken, defaultName, saving, error, onClose, onSave }: ArtifactSaveSheetProps) {
  const browser = useDriveBrowser(visible, sessionToken);
  const [folderId, setFolderId] = useState('');
  const [fileName, setFileName] = useState(defaultName);

  useEffect(() => {
    if (!visible) return;
    setFolderId('');
    setFileName(defaultName);
  }, [defaultName, visible]);

  const folders = useMemo(
    () => driveFolderChildren(browser.folders, folderId),
    [browser.folders, folderId],
  );
  const cleanName = fileName.trim();

  return (
    <Modal visible={visible} animationType="slide" presentationStyle="formSheet" onRequestClose={onClose}>
      <SheetFrame title="Save to Drive" subtitle={locationName(browser.folders, folderId)} onClose={onClose}>
        <View style={styles.form}>
          <Text style={styles.fieldLabel}>FILE NAME</Text>
          <TextInput
            accessibilityLabel="Drive file name"
            editable={!saving}
            onChangeText={setFileName}
            placeholder="File name"
            placeholderTextColor={colors.text3}
            selectionColor={colors.info}
            selectTextOnFocus
            style={styles.nameInput}
            value={fileName}
          />
          <View style={styles.folderHeading}>
            <Text style={styles.fieldLabel}>FOLDER</Text>
            {folderId ? (
              <Pressable accessibilityRole="button" accessibilityLabel="Go to parent folder" disabled={saving} onPress={() => setFolderId(parentFolderId(browser.folders, folderId))} style={({ pressed }) => [styles.back, pressed && styles.pressed]}>
                <SymbolView name="chevron.left" tintColor={colors.text2} size={13} />
                <Text style={styles.backText}>Back</Text>
              </Pressable>
            ) : null}
          </View>
          <ScrollView contentContainerStyle={styles.folderList}>
            {browser.loading ? <ActivityIndicator color={colors.emberText} /> : null}
            {folders.map((folder) => (
              <Pressable key={folder.id} accessibilityRole="button" accessibilityLabel={`Open ${folder.name}`} disabled={saving} onPress={() => setFolderId(folder.id)} style={({ pressed }) => [styles.row, pressed && styles.pressed]}>
                <View style={styles.rowIcon}><SymbolView name="folder.fill" tintColor={colors.emberText} size={19} /></View>
                <Text numberOfLines={1} style={styles.rowName}>{folder.name}</Text>
                <SymbolView name="chevron.right" tintColor={colors.text3} size={13} />
              </Pressable>
            ))}
            {!browser.loading && folders.length === 0 ? <Text style={styles.locationHint}>Save here, or go back to choose another folder.</Text> : null}
          </ScrollView>
          {browser.error || error ? <Text accessibilityRole="alert" style={styles.error}>{browser.error || error}</Text> : null}
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={`Save ${cleanName || 'file'} to ${locationName(browser.folders, folderId)}`}
            accessibilityState={{ disabled: saving || !cleanName }}
            disabled={saving || !cleanName}
            onPress={() => onSave(cleanName, folderId)}
            style={({ pressed }) => [styles.savePrimary, pressed && styles.pressed, (saving || !cleanName) && styles.disabled]}
          >
            {saving ? <ActivityIndicator color={colors.onAccent} size="small" /> : <SymbolView name="externaldrive.fill" tintColor={colors.onAccent} size={17} />}
            <Text style={styles.primaryText}>{saving ? 'Saving…' : 'Save here'}</Text>
          </Pressable>
        </View>
      </SheetFrame>
    </Modal>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, overflow: 'hidden', backgroundColor: colors.bgApp },
  handle: { alignSelf: 'center', width: 36, height: 5, marginTop: space[2], borderRadius: radius.full, backgroundColor: colors.line2 },
  header: { minHeight: 78, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[5], borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: colors.line1 },
  headerCopy: { minWidth: 0, flex: 1, gap: 2 },
  eyebrow: { ...type.label, color: colors.emberText, letterSpacing: 0.6 },
  title: { ...type.title2, color: colors.text1 },
  subtitle: { ...type.caption, color: colors.text3 },
  close: { width: hitMin, height: hitMin, alignItems: 'center', justifyContent: 'center', borderRadius: radius.full, borderCurve: 'continuous', backgroundColor: colors.surface3 },
  pressed: { opacity: 0.72, transform: [{ scale: 0.96 }] },
  disabled: { opacity: 0.46 },
  searchFrame: { minHeight: hitMin, flexDirection: 'row', alignItems: 'center', gap: space[2], marginHorizontal: space[4], marginTop: space[3], paddingHorizontal: space[3], borderRadius: radius.lg, borderCurve: 'continuous', backgroundColor: colors.surface3 },
  searchInput: { ...type.body, minHeight: hitMin, flex: 1, paddingVertical: 0, color: colors.text1 },
  locationBar: { minHeight: hitMin, justifyContent: 'center', paddingHorizontal: space[4] },
  locationHint: { ...type.caption, color: colors.text3 },
  back: { minHeight: hitMin, alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', gap: 6, paddingRight: space[3] },
  backText: { ...type.captionMedium, color: colors.text2 },
  list: { flexGrow: 1, gap: space[2], paddingHorizontal: space[4], paddingBottom: space[8] },
  row: { ...shadow[1], minHeight: 58, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[3], borderRadius: radius.lg, borderCurve: 'continuous', backgroundColor: colors.surface1 },
  rowSelected: { backgroundColor: colors.emberSoft },
  rowIcon: { width: 36, height: 36, alignItems: 'center', justifyContent: 'center', borderRadius: radius.md, borderCurve: 'continuous', backgroundColor: colors.surface3 },
  rowIconSelected: { backgroundColor: colors.accent },
  rowCopy: { minWidth: 0, flex: 1 },
  rowName: { ...type.bodyMedium, minWidth: 0, flex: 1, color: colors.text1 },
  rowMeta: { ...type.caption, color: colors.text3 },
  retry: { minHeight: 92, alignItems: 'center', justifyContent: 'center', gap: space[2] },
  error: { ...type.caption, color: colors.danger },
  retryText: { ...type.captionMedium, color: colors.info },
  empty: { minHeight: 170, alignItems: 'center', justifyContent: 'center', gap: space[2] },
  emptyTitle: { ...type.bodyMedium, color: colors.text2 },
  footer: { minHeight: 72, flexDirection: 'row', alignItems: 'center', gap: space[3], paddingHorizontal: space[4], borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: colors.line1, backgroundColor: colors.bgApp },
  selectionCount: { ...type.captionMedium, flex: 1, color: colors.text2, fontVariant: ['tabular-nums'] },
  primary: { minWidth: 112, minHeight: hitMin, alignItems: 'center', justifyContent: 'center', paddingHorizontal: space[4], borderRadius: radius.full, backgroundColor: colors.accent },
  primaryText: { ...type.captionMedium, color: colors.onAccent },
  form: { flex: 1, gap: space[3], padding: space[5] },
  fieldLabel: { ...type.label, color: colors.text3, letterSpacing: 0.5 },
  nameInput: { ...type.body, minHeight: 52, paddingHorizontal: space[3], borderRadius: radius.lg, borderCurve: 'continuous', borderWidth: StyleSheet.hairlineWidth, borderColor: colors.line2, color: colors.text1, backgroundColor: colors.surface1 },
  folderHeading: { minHeight: hitMin, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' },
  folderList: { gap: space[2], paddingBottom: space[3] },
  savePrimary: { minHeight: 52, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: space[2], borderRadius: radius.full, backgroundColor: colors.accent },
});
