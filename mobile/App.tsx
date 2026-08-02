import React, { useEffect } from 'react';
import {
  GoogleSansFlex_400Regular,
  GoogleSansFlex_500Medium,
  GoogleSansFlex_600SemiBold,
  GoogleSansFlex_700Bold,
} from '@expo-google-fonts/google-sans-flex';
import {
  GeistMono_400Regular,
  GeistMono_500Medium,
  GeistMono_600SemiBold,
} from '@expo-google-fonts/geist-mono';
import { useFonts } from 'expo-font';
import { StatusBar } from 'expo-status-bar';
import { Appearance, useColorScheme } from 'react-native';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { AuthProvider, useAuth } from './src/auth/AuthContext';
import { LaunchCradle } from './src/components/CanvasCradleComposition';
import './src/theme/installTypography';
import { RootNavigator } from './src/navigation/RootNavigator';
import { OfficeEventsProvider } from './src/realtime/OfficeEventsContext';

function AdaptiveStatusBar() {
  const system = useColorScheme();
  const { themePreference: preference } = useAuth();
  useEffect(() => {
    const colorScheme = preference === 'dark' ? 'dark' : preference === 'light' ? 'light' : 'unspecified';
    Appearance.setColorScheme(colorScheme);
  }, [preference]);
  const dark = preference === 'dark' || (preference === 'system' && system === 'dark');
  return <StatusBar style={dark ? 'light' : 'dark'} />;
}

export default function App() {
  const [fontsLoaded, fontError] = useFonts({
    GoogleSansFlex_400Regular,
    GoogleSansFlex_500Medium,
    GoogleSansFlex_600SemiBold,
    GoogleSansFlex_700Bold,
    GeistMono_400Regular,
    GeistMono_500Medium,
    GeistMono_600SemiBold,
  });

  if (!fontsLoaded && !fontError) return <LaunchCradle />;

  return (
    <SafeAreaProvider>
      <AuthProvider>
        <OfficeEventsProvider>
          <AdaptiveStatusBar />
          <RootNavigator />
        </OfficeEventsProvider>
      </AuthProvider>
    </SafeAreaProvider>
  );
}
