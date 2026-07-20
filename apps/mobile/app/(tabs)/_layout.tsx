import useAuth from '@/context/auth';
import { usePushRegistration } from '@/hooks/use-push-registration';
import { useServerConfig } from '@/lib/server-config';
import { THEME } from '@/lib/theme';
import { Redirect, Tabs, useRouter } from 'expo-router';
import {
  CalendarDaysIcon,
  InboxIcon,
  ListTodoIcon,
  Settings2Icon,
  SparklesIcon,
} from 'lucide-react-native';
import { useColorScheme } from 'nativewind';
import { ActivityIndicator, View } from 'react-native';

export default function TabsLayout() {
  const { user, loading } = useAuth();
  const { config } = useServerConfig();
  const router = useRouter();
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme ?? 'light'];
  const aiEnabled = config?.features?.ai ?? false;

  usePushRegistration();

  if (loading) {
    return (
      <View className="flex-1 items-center justify-center bg-background">
        <ActivityIndicator size="large" />
      </View>
    );
  }

  if (!user) {
    return <Redirect href="/" />;
  }

  return (
    <Tabs
      screenOptions={{
        headerShown: false,
        tabBarActiveTintColor: theme.foreground,
        tabBarInactiveTintColor: theme.mutedForeground,
        tabBarStyle: {
          backgroundColor: theme.background,
          borderTopColor: theme.border,
        },
      }}>
      <Tabs.Screen
        name="inbox"
        options={{
          title: 'Inbox',
          tabBarIcon: ({ color, size }) => <InboxIcon color={color} size={size ?? 22} />,
        }}
      />
      <Tabs.Screen
        name="calendar"
        options={{
          title: 'Calendar',
          tabBarIcon: ({ color, size }) => <CalendarDaysIcon color={color} size={size ?? 22} />,
        }}
      />
      <Tabs.Screen
        name="tasks"
        options={{
          title: 'Tasks',
          tabBarIcon: ({ color, size }) => <ListTodoIcon color={color} size={size ?? 22} />,
        }}
      />
      <Tabs.Screen
        name="settings"
        options={{
          title: 'Settings',
          tabBarIcon: ({ color, size }) => <Settings2Icon color={color} size={size ?? 22} />,
        }}
      />
      {/* Mobile has no persistent AI sidebar; Ask AI is a modal reached from
          this tab bar button instead (hidden when the server disables AI). */}
      {aiEnabled && (
        <Tabs.Screen
          name="ask-ai"
          options={{
            title: 'Ask AI',
            tabBarIcon: ({ color, size }) => <SparklesIcon color={color} size={size ?? 22} />,
          }}
          listeners={{
            tabPress: (e) => {
              e.preventDefault();
              router.push('/ask-ai');
            },
          }}
        />
      )}
    </Tabs>
  );
}
