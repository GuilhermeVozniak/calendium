import { createIndexedDbKv } from '@calendium/shared';
import { QueryClient } from '@tanstack/react-query';
import { createAsyncStoragePersister } from '@tanstack/query-async-storage-persister';
import { PersistQueryClientProvider } from '@tanstack/react-query-persist-client';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import { Loader2 } from 'lucide-react';

import App from './App';
import { useSession } from './lib/auth';
import { initNamedTheme } from './lib/named-theme';
import { QUERY_CACHE_STORAGE_KEY, startOutboxReplay } from './lib/offline';
import { ServerConfigProvider, useServerConfig } from './lib/server-config';
import { ConnectView } from './views/ConnectView';
import { SignInView } from './views/SignInView';
import './styles.css';

// Light + dark themes follow the OS (design language mandate: both are
// first-class everywhere).
const media = window.matchMedia('(prefers-color-scheme: dark)');
const applyTheme = () => {
  document.documentElement.classList.toggle('dark', media.matches);
};
applyTheme();
media.addEventListener('change', applyTheme);

// Named palette (data-theme on <html>, M2.6 Task 13): apply the locally
// stored preference before first render; the Settings view re-syncs it from
// the server.
initNamedTheme();

// Persisted query cache (M2.6): the last-known server data survives restarts
// so the app opens instantly offline. gcTime must outlive maxAge or persisted
// queries would be garbage-collected before they can be restored.
const CACHE_MAX_AGE_MS = 24 * 60 * 60 * 1000;
// Bump to discard every persisted cache after a breaking shape change
// (mirrors the web app's buster).
const CACHE_BUSTER = 'calendium-cache-v1';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 30_000,
      gcTime: CACHE_MAX_AGE_MS,
      refetchOnWindowFocus: false,
    },
  },
});

// The Wails webview (WKWebView/WebView2) fully supports IndexedDB, so the
// same KV that backs the outbox also backs the query-cache persister.
const persister = createAsyncStoragePersister({
  storage: createIndexedDbKv(),
  key: QUERY_CACHE_STORAGE_KEY,
});

// Replays queued offline actions now (if online) and on every reconnect.
startOutboxReplay(queryClient);

// Two gates: first pick a server (Connect), then authenticate against its
// Better Auth instance (Sign in). Only a configured server + live session
// mounts the app (which builds the Better Auth + API clients from that config).
function Root() {
  const { isConfigured, demoMode } = useServerConfig();
  // Explicit "Try the demo" skips the Connect + Sign-in gates and runs on mock data.
  if (demoMode) return <App />;
  if (!isConfigured) return <ConnectView />;
  return <AuthGate />;
}

function AuthGate() {
  const { user, isPending } = useSession();
  if (isPending) {
    return (
      <div className="flex h-full flex-col overflow-hidden bg-background">
        <header className="titlebar-drag h-10 shrink-0" />
        <div className="flex min-h-0 flex-1 items-center justify-center">
          <Loader2 className="size-5 animate-spin text-muted-foreground" />
        </div>
      </div>
    );
  }
  return user ? <App /> : <SignInView />;
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <PersistQueryClientProvider
      client={queryClient}
      persistOptions={{ persister, buster: CACHE_BUSTER, maxAge: CACHE_MAX_AGE_MS }}
    >
      <ServerConfigProvider>
        <Root />
      </ServerConfigProvider>
    </PersistQueryClientProvider>
  </StrictMode>
);
