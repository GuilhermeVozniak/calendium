import { focusManager } from '@tanstack/react-query';
import { AppState, type AppStateStatus, Platform } from 'react-native';

/**
 * React Native has no window focus events, so react-query never refetches on
 * "focus" by itself. Map AppState to focusManager (the standard TanStack RN
 * pattern): foregrounding the app refetches stale queries — notably the
 * billing gate's ['subscription'], so a user who subscribed elsewhere is let
 * back in without restarting the app. Returns the unsubscribe function.
 */
export function startAppStateFocus(): () => void {
  const sub = AppState.addEventListener('change', (status: AppStateStatus) => {
    if (Platform.OS !== 'web') focusManager.setFocused(status === 'active');
  });
  return () => sub.remove();
}
