import React, { useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  Modal,
  Pressable,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { SafeAreaView } from 'react-native-safe-area-context';
import { WebView } from 'react-native-webview';
import { SymbolView } from 'expo-symbols';
import {
  authenticatedFileHeaders,
  authenticatedFileUrl,
  shareOrSaveRemoteFile,
  type RemoteFile,
} from '../files/fileActions';
import { colors, hitMin, radius, space, type } from '../theme/tokens';

type Props = {
  file: RemoteFile | null;
  sessionToken: string;
  onClose: () => void;
};

export function FilePreviewModal({ file, sessionToken, onClose }: Props) {
  const [loading, setLoading] = useState(true);
  const [sharing, setSharing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const url = file ? authenticatedFileUrl(file) : '';

  async function share() {
    if (!file || sharing) return;
    setSharing(true);
    try {
      await shareOrSaveRemoteFile(sessionToken, file);
    } catch (caught) {
      Alert.alert(
        'Could not share this file',
        caught instanceof Error ? caught.message : 'Please try again.',
      );
    } finally {
      setSharing(false);
    }
  }

  return (
    <Modal
      visible={Boolean(file && url)}
      animationType="slide"
      presentationStyle="fullScreen"
      onRequestClose={onClose}
      onShow={() => {
        setLoading(true);
        setError(null);
      }}
    >
      <SafeAreaView style={styles.safe} edges={['top', 'right', 'bottom', 'left']}>
        <View style={styles.toolbar}>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Close file preview"
            hitSlop={4}
            onPress={onClose}
            style={({ pressed }) => [styles.toolbarButton, pressed && styles.pressed]}
          >
            <SymbolView name="xmark" tintColor={colors.text1} size={17} />
          </Pressable>
          <Text numberOfLines={1} style={styles.title}>{file?.name ?? ''}</Text>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={`Share or save ${file?.name ?? 'file'}`}
            disabled={sharing}
            hitSlop={4}
            onPress={() => void share()}
            style={({ pressed }) => [
              styles.toolbarButton,
              pressed && styles.pressed,
              sharing && styles.disabled,
            ]}
          >
            {sharing ? (
              <ActivityIndicator size="small" color={colors.text1} />
            ) : (
              <SymbolView name="square.and.arrow.up" tintColor={colors.text1} size={19} />
            )}
          </Pressable>
        </View>
        <View style={styles.preview}>
          {file && url ? (
            <WebView
              source={{
                uri: url,
                headers: authenticatedFileHeaders(sessionToken, file.mime),
              }}
              style={styles.web}
              javaScriptEnabled={false}
              sharedCookiesEnabled={false}
              allowsBackForwardNavigationGestures={false}
              setSupportMultipleWindows={false}
              onLoadStart={() => {
                setLoading(true);
                setError(null);
              }}
              onLoadEnd={() => setLoading(false)}
              onError={() => {
                setLoading(false);
                setError('The preview could not be loaded. You can still share or save the file.');
              }}
              onHttpError={(event) => {
                setLoading(false);
                setError(`The file server returned ${event.nativeEvent.statusCode}.`);
              }}
            />
          ) : null}
          {loading ? (
            <View style={styles.loading} pointerEvents="none">
              <ActivityIndicator color={colors.accent} />
            </View>
          ) : null}
          {error ? (
            <View style={styles.error} accessibilityRole="alert">
              <SymbolView name="exclamationmark.circle.fill" tintColor={colors.danger} size={18} />
              <Text style={styles.errorText}>{error}</Text>
            </View>
          ) : null}
        </View>
      </SafeAreaView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  safe: {
    flex: 1,
    backgroundColor: colors.bgApp,
  },
  toolbar: {
    minHeight: 58,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[3],
    paddingHorizontal: space[3],
    backgroundColor: colors.surface1,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: colors.line1,
  },
  toolbarButton: {
    width: hitMin,
    height: hitMin,
    borderRadius: radius.md,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.surface3,
  },
  title: {
    flex: 1,
    ...type.headline,
    color: colors.text1,
    textAlign: 'center',
  },
  preview: {
    flex: 1,
    backgroundColor: colors.surface3,
  },
  web: {
    flex: 1,
    backgroundColor: colors.surface3,
  },
  loading: {
    ...StyleSheet.absoluteFill,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.surface3,
  },
  error: {
    position: 'absolute',
    left: space[4],
    right: space[4],
    bottom: space[4],
    minHeight: hitMin,
    flexDirection: 'row',
    alignItems: 'center',
    gap: space[2],
    paddingHorizontal: space[3],
    paddingVertical: space[2],
    borderRadius: radius.md,
    backgroundColor: colors.dangerSoft,
  },
  errorText: {
    flex: 1,
    ...type.caption,
    color: colors.danger,
  },
  pressed: {
    opacity: 0.72,
    transform: [{ scale: 0.96 }],
  },
  disabled: {
    opacity: 0.5,
  },
});
