import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import App from './App';
import { ServerConfigProvider, useServerConfig } from './lib/server-config';
import { ConnectView } from './views/ConnectView';
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

// Server-discovery gate: show Connect until a Calendium server is configured,
// then mount the app (which builds Supabase + API clients from that config).
function Root() {
  const { isConfigured } = useServerConfig();
  return isConfigured ? <App /> : <ConnectView />;
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
