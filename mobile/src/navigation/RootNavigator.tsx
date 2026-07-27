import React from 'react';
import { ActivityIndicator, StyleSheet, View, useColorScheme } from 'react-native';
import { SymbolView, type SFSymbol } from 'expo-symbols';
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
import { MoreScreen } from '../screens/MoreScreen';
import { RoomScreen } from '../screens/RoomScreen';
import { CreateRoomScreen } from '../screens/CreateRoomScreen';
import { ThreadScreen } from '../screens/ThreadScreen';
import { AlertsScreen } from '../screens/AlertsScreen';
import {
  FilesScreen,
  IntelligenceScreen,
  MeetingsScreen,
  MemoryScreen,
} from '../screens/LibraryScreens';
import { SettingsScreen } from '../screens/SettingsScreen';
import { MomentumGlyph } from '../components/BrandMark';
import { colors, product } from '../theme/tokens';
import type { MainTabParamList, RootStackParamList } from './types';

const Stack = createNativeStackNavigator<RootStackParamList>();
const Tab = createBottomTabNavigator<MainTabParamList>();

/**
 * Native tab shell for the high-frequency mobile surfaces. Rich desktop-only
 * canvases remain available from More without becoming the app shell.
 */
function MainTabs() {
  const dark = useColorScheme() === 'dark';
  const icons: Record<Exclude<keyof MainTabParamList, 'Home'>, SFSymbol> = {
    Rooms: 'person.2.fill',
    Chat: 'bubble.left.and.bubble.right.fill',
    Board: 'rectangle.3.group.fill',
    More: 'ellipsis.circle.fill',
  };
  return (
    <Tab.Navigator
      screenOptions={({ route }) => ({
        headerShown: false,
        tabBarActiveTintColor: dark ? '#F7F7F9' : '#0E0E10',
        tabBarInactiveTintColor: dark ? 'rgba(247,247,249,0.42)' : 'rgba(14,14,16,0.38)',
        tabBarStyle: {
          backgroundColor: colors.glassPanel,
          borderTopColor: colors.line1,
          borderTopWidth: StyleSheet.hairlineWidth,
        },
        tabBarLabelStyle: {
          fontSize: 11,
          fontWeight: '500',
          letterSpacing: 0.2,
        },
        tabBarIcon: ({ color, size }) => {
          const iconSize = Math.min(size, 22);
          if (route.name === 'Home') {
            return (
              <MomentumGlyph
                size={iconSize}
                color={color}
              />
            );
          }
          return <SymbolView name={icons[route.name]} tintColor={color} size={iconSize} />;
        },
      })}
    >
      <Tab.Screen
        name="Home"
        component={HomeScreen}
        options={{ title: product.name }}
      />
      <Tab.Screen name="Rooms" component={RoomsScreen} options={{ title: 'Rooms' }} />
      <Tab.Screen name="Chat" component={ScoutScreen} options={{ title: 'Chat' }} />
      <Tab.Screen name="Board" component={BoardScreen} options={{ title: 'Board' }} />
      <Tab.Screen name="More" component={MoreScreen} options={{ title: 'More' }} />
    </Tab.Navigator>
  );
}

export function RootNavigator() {
  const { user, bootstrapping } = useAuth();
  const dark = useColorScheme() === 'dark';
  const navTheme = {
    ...DefaultTheme,
    dark,
    colors: {
      ...DefaultTheme.colors,
      background: dark ? '#09090B' : '#F5F5F7',
      card: dark ? '#141418' : '#FFFFFF',
      text: dark ? '#F7F7F9' : '#0E0E10',
      border: dark ? 'rgba(255,255,255,0.09)' : 'rgba(14,14,16,0.08)',
      primary: dark ? '#F7F7F9' : '#0E0E10',
    },
  };

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
            <Stack.Screen name="Room" component={RoomScreen} />
            <Stack.Screen name="CreateRoom" component={CreateRoomScreen} options={{ presentation: 'formSheet' }} />
            <Stack.Screen name="Thread" component={ThreadScreen} />
            <Stack.Screen name="Intelligence" component={IntelligenceScreen} />
            <Stack.Screen name="Memory" component={MemoryScreen} />
            <Stack.Screen name="Meetings" component={MeetingsScreen} />
            <Stack.Screen name="Files" component={FilesScreen} />
            <Stack.Screen name="Alerts" component={AlertsScreen} />
            <Stack.Screen name="Settings" component={SettingsScreen} />
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
