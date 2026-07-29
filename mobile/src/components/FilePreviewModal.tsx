import React, { useEffect, useState } from 'react';
import {
  ActivityIndicator,
  Alert,
  Modal,
  Pressable,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { Image } from 'expo-image';
import { WebView } from 'react-native-webview';
import { SymbolView } from 'expo-symbols';
import {
  authenticatedFileUrl,
  downloadRemoteFile,
  shareOrSaveRemoteFile,
  type RemoteFile,
} from '../files/fileActions';
import { colors, hitMin, radius, space, type } from '../theme/tokens';
import { useReduceMotion } from '../theme/motion';

type Props = {
  file: RemoteFile | null;
  sessionToken: string;
  onClose: () => void;
};

export function FilePreviewModal({ file, sessionToken, onClose }: Props) {
  const insets = useSafeAreaInsets();
  const reduceMotion = useReduceMotion();
  const [loading, setLoading] = useState(true);
  const [sharing, setSharing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [localPreviewUrl, setLocalPreviewUrl] = useState('');
  const url = file ? authenticatedFileUrl(file) : '';
  const image = Boolean(file?.mime?.toLowerCase().startsWith('image/'));

  useEffect(() => {
    let active = true;
    setLocalPreviewUrl('');
    if (!file || !url || !sessionToken) return () => { active = false; };
    setLoading(true);
    setError(null);
    void downloadRemoteFile(sessionToken, file)
      .then((downloaded) => {
        if (active) setLocalPreviewUrl(downloaded.uri);
      })
      .catch((caught) => {
        if (!active) return;
        setLoading(false);
        setError(caught instanceof Error ? caught.message : 'The file could not be downloaded.');
      });
    return () => { active = false; };
  }, [file?.name, file?.ref, sessionToken, url]);

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
      animationType={reduceMotion ? 'none' : 'slide'}
      presentationStyle="fullScreen"
      statusBarTranslucent
      navigationBarTranslucent
      onRequestClose={onClose}
    >
      <View
        style={[
          styles.safe,
          image && styles.mediaSafe,
          !image && {
            paddingTop: insets.top,
            paddingRight: insets.right,
            paddingBottom: insets.bottom,
            paddingLeft: insets.left,
          },
        ]}
      >
        <View style={[
          styles.toolbar,
          image && styles.mediaToolbar,
          image && {
            minHeight: insets.top + 60,
            paddingTop: insets.top + space[2],
            paddingRight: insets.right + space[3],
            paddingLeft: insets.left + space[3],
          },
        ]}>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel="Close file preview"
            hitSlop={4}
            onPress={onClose}
            style={({ pressed }) => [styles.toolbarButton, image && styles.mediaButton, pressed && styles.pressed]}
          >
            <SymbolView name="xmark" tintColor={image ? '#FFFFFF' : colors.text1} size={17} />
          </Pressable>
          {image ? <View style={styles.mediaTitleSpacer} /> : (
            <Text numberOfLines={1} style={styles.title}>{file?.name ?? ''}</Text>
          )}
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={`Share or save ${file?.name ?? 'file'}`}
            disabled={sharing}
            hitSlop={4}
            onPress={() => void share()}
            style={({ pressed }) => [
              styles.toolbarButton,
              image && styles.mediaButton,
              pressed && styles.pressed,
              sharing && styles.disabled,
            ]}
          >
            {sharing ? (
              <ActivityIndicator size="small" color={image ? '#FFFFFF' : colors.text1} />
            ) : (
              <SymbolView name="square.and.arrow.up" tintColor={image ? '#FFFFFF' : colors.text1} size={19} />
            )}
          </Pressable>
        </View>
        <View style={[styles.preview, image && styles.mediaPreview]}>
          {file && localPreviewUrl && image ? (
            <Image
              source={{ uri: localPreviewUrl }}
              accessibilityLabel={`Preview of ${file.name}`}
              cachePolicy="memory-disk"
              contentFit="contain"
              recyclingKey={`${file.ref}-full-preview`}
              onLoadStart={() => {
                setLoading(true);
                setError(null);
              }}
              onLoad={() => setLoading(false)}
              onDisplay={() => setLoading(false)}
              onError={() => {
                setLoading(false);
                setError('The image preview could not be loaded. You can still share or save the file.');
              }}
              style={styles.image}
            />
          ) : file && localPreviewUrl ? (
            <WebView
              source={{ uri: localPreviewUrl }}
              originWhitelist={['file://*']}
              allowFileAccess
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
            <View style={[styles.loading, image && styles.mediaLoading]} pointerEvents="none">
              <ActivityIndicator color={colors.accent} />
            </View>
          ) : null}
          {error ? (
            <View style={[styles.error, image && { bottom: insets.bottom + space[4] }]} accessibilityRole="alert">
              <SymbolView name="exclamationmark.circle.fill" tintColor={colors.danger} size={18} />
              <Text style={styles.errorText}>{error}</Text>
            </View>
          ) : null}
        </View>
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  safe: {
    flex: 1,
    backgroundColor: colors.bgApp,
  },
  mediaSafe: { backgroundColor: '#000000' },
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
  mediaToolbar: {
    position: 'absolute',
    zIndex: 4,
    top: 0,
    left: 0,
    right: 0,
    borderBottomWidth: 0,
    backgroundColor: 'transparent',
  },
  toolbarButton: {
    width: hitMin,
    height: hitMin,
    borderRadius: radius.md,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.surface3,
  },
  mediaButton: { backgroundColor: 'rgba(8,8,10,0.66)', borderWidth: StyleSheet.hairlineWidth, borderColor: 'rgba(255,255,255,0.18)' },
  title: {
    flex: 1,
    ...type.headline,
    color: colors.text1,
    textAlign: 'center',
  },
  mediaTitleSpacer: { flex: 1 },
  preview: {
    flex: 1,
    backgroundColor: colors.surface3,
  },
  mediaPreview: { backgroundColor: '#000000' },
  web: {
    flex: 1,
    backgroundColor: colors.surface3,
  },
  image: {
    flex: 1,
    backgroundColor: '#000000',
  },
  loading: {
    ...StyleSheet.absoluteFill,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.surface3,
  },
  mediaLoading: { backgroundColor: 'rgba(0,0,0,0.16)' },
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
