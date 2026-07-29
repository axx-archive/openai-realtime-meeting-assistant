import React, { useEffect } from 'react';
import { StatusBar } from 'expo-status-bar';
import { Appearance, useColorScheme } from 'react-native';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { AuthProvider, useAuth } from './src/auth/AuthContext';
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
