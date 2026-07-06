import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import { Loader2 } from 'lucide-react';

import App from './App';
import { useSession } from './lib/auth';
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

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 30_000,
      refetchOnWindowFocus: false,
    },
  },
});

// Two gates: first pick a server (Connect), then authenticate against its
// Better Auth instance (Sign in). Only a configured server + live session
// mounts the app (which builds the Better Auth + API clients from that config).
function Root() {
  const { isConfigured } = useServerConfig();
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
    <QueryClientProvider client={queryClient}>
      <ServerConfigProvider>
        <Root />
      </ServerConfigProvider>
    </QueryClientProvider>
  </StrictMode>
);
