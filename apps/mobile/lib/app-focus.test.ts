import { focusManager } from '@tanstack/react-query';
import { AppState, type AppStateStatus } from 'react-native';
import { startAppStateFocus } from './app-focus';

afterEach(() => {
  jest.restoreAllMocks();
  focusManager.setFocused(undefined);
});

describe('startAppStateFocus', () => {
  it('drives react-query focus from AppState and unsubscribes on cleanup', () => {
    let handler: ((s: AppStateStatus) => void) | undefined;
    const remove = jest.fn();
    jest.spyOn(AppState, 'addEventListener').mockImplementation((type, fn) => {
      expect(type).toBe('change');
      handler = fn as (s: AppStateStatus) => void;
      return { remove } as ReturnType<typeof AppState.addEventListener>;
    });

    const stop = startAppStateFocus();
    expect(handler).toBeDefined();

    handler?.('background');
    expect(focusManager.isFocused()).toBe(false);
    handler?.('active');
    expect(focusManager.isFocused()).toBe(true);
    handler?.('inactive');
    expect(focusManager.isFocused()).toBe(false);

    stop();
    expect(remove).toHaveBeenCalledTimes(1);
  });
});
