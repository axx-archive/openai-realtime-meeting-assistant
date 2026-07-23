import React from 'react';
import { ActivityIndicator, Platform, StyleSheet, View } from 'react-native';
import { NavigationContainer, DefaultTheme } from '@react-navigation/native';
import { createBottomTabNavigator } from '@react-navigation/bottom-tabs';
import { createNativeStackNavigator } from '@react-navigation/native-stack';
import { useAuth } from '../auth/AuthContext';
import { BoardScreen } from '../screens/BoardScreen';
import { HomeScreen } from '../screens/HomeScreen';
import { LoginScreen } from '../screens/LoginScreen';
import { OSWebScreen } from '../screens/OSWebScreen';
import { RoomsScreen } from '../screens/RoomsScreen';
import { ScoutScreen } from '../screens/ScoutScreen';
import { colors, product } from '../theme/tokens';
import type { MainTabParamList, RootStackParamList } from './types';

const Stack = createNativeStackNavigator<RootStackParamList>();
const Tab = createBottomTabNavigator<MainTabParamList>();

const navTheme = {
  ...DefaultTheme,
  colors: {
    ...DefaultTheme.colors,
    background: colors.bgApp,
    card: colors.surface1,
    text: colors.text1,
    border: colors.line1,
    primary: colors.accent,
  },
};

/**
 * Bottom tabs approximate the live mobile glass tool island labels.
 * Full OS remains available via WebView for tools not yet natively ported
 * (Intelligence, Memory, Files, Room media).
 */
function MainTabs() {
  return (
    <Tab.Navigator
      screenOptions={{
        headerShown: false,
        tabBarActiveTintColor: colors.accent,
        tabBarInactiveTintColor: colors.tabInactive,
        tabBarStyle: {
          backgroundColor: Platform.OS === 'ios' ? 'rgba(255,255,255,0.86)' : colors.surface1,
          borderTopColor: colors.line1,
          borderTopWidth: StyleSheet.hairlineWidth,
        },
        tabBarLabelStyle: {
          fontSize: 11,
          fontWeight: '500',
          letterSpacing: 0.2,
        },
      }}
    >
      <Tab.Screen
        name="Home"
        component={HomeScreen}
        options={{ title: product.name }}
      />
      <Tab.Screen name="Rooms" component={RoomsScreen} options={{ title: 'Rooms' }} />
      <Tab.Screen name="Chat" component={ScoutScreen} options={{ title: 'Chat' }} />
      <Tab.Screen name="Board" component={BoardScreen} options={{ title: 'Board' }} />
    </Tab.Navigator>
  );
}

export function RootNavigator() {
  const { user, bootstrapping } = useAuth();

  if (bootstrapping) {
    return (
      <View style={styles.boot}>
        <ActivityIndicator size="large" color={colors.accent} />
      </View>
    );
  }

  return (
    <NavigationContainer theme={navTheme}>
      <Stack.Navigator screenOptions={{ headerShown: false }}>
        {user ? (
          <>
            <Stack.Screen name="Main" component={MainTabs} />
            <Stack.Screen
              name="OSWeb"
              component={OSWebScreen}
              options={{ presentation: 'modal', animation: 'slide_from_bottom' }}
            />
          </>
        ) : (
          <Stack.Screen name="Login" component={LoginScreen} />
        )}
      </Stack.Navigator>
    </NavigationContainer>
  );
}

const styles = StyleSheet.create({
  boot: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.bgApp,
  },
});
