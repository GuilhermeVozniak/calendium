import { CheckCircle2, Cloud, Loader2, Server } from 'lucide-react';
import { type FormEvent, useState } from 'react';

import {
  CLOUD_PRESET,
  discoverServer,
  useServerConfig,
  type ServerConfig,
} from '@/lib/server-config';
import { Button } from '@/ui/button';
import { Input } from '@/ui/input';

/**
 * Server-discovery gate (open-core): shown until the desktop client is pointed
 * at a Calendium server. The user enters a server URL (or picks Calendium
 * Cloud); we fetch its public /v1/instance descriptor, confirm the instance,
 * and persist the config so the Better Auth + API clients build at runtime.
 */
export function ConnectView() {
  const { save } = useServerConfig();
  const [url, setUrl] = useState((import.meta.env.VITE_API_URL as string | undefined) ?? '');
  const [busy, setBusy] = useState<null | 'server' | 'cloud'>(null);
  const [error, setError] = useState<string | null>(null);
  const [discovered, setDiscovered] = useState<ServerConfig | null>(null);

  async function connect(target: string, kind: 'server' | 'cloud') {
    if (!target.trim()) {
      setError('Enter your Calendium server URL.');
      return;
    }
    setError(null);
    setBusy(kind);
    try {
      setDiscovered(await discoverServer(target));
    } catch (e) {
      setError(
        e instanceof Error
          ? `Couldn't reach a Calendium server there. ${e.message}`
          : "Couldn't reach a Calendium server there."
      );
    } finally {
      setBusy(null);
    }
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    void connect(url, 'server');
  }

  return (
    <div className="flex h-full flex-col overflow-hidden bg-background">
      {/* Draggable strip so the frameless window stays movable. */}
      <header className="titlebar-drag h-10 shrink-0" />
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <div className="w-full max-w-sm">
          {/* Brand mark */}
          <div className="mb-8 flex flex-col items-center gap-3 text-center">
            <div className="flex size-14 items-center justify-center rounded-2xl bg-primary text-primary-foreground">
              <Server className="size-7" />
            </div>
            <div>
              <h1 className="text-lg font-semibold tracking-tight">Connect to your server</h1>
              <p className="mt-1 text-sm text-muted-foreground">
                Point Calendium at your own server, or use Calendium Cloud.
              </p>
            </div>
          </div>

          {discovered ? (
            <div className="flex flex-col gap-4 rounded-lg border bg-card p-5 shadow-sm">
              <div className="flex items-center gap-3">
                <CheckCircle2 className="size-5 text-primary" />
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium">{discovered.name}</div>
                  <div className="truncate text-xs text-muted-foreground">
                    {discovered.mode === 'self_host' ? 'Self-hosted instance' : 'Calendium Cloud'}
                    {' · '}
                    {discovered.serverUrl}
                  </div>
                </div>
              </div>
              <div className="flex items-center gap-2">
                <Button size="sm" onClick={() => save(discovered)}>
                  Continue
                </Button>
                <Button variant="ghost" size="sm" onClick={() => setDiscovered(null)}>
                  Use a different server
                </Button>
              </div>
            </div>
          ) : (
            <div className="flex flex-col gap-4">
              <form onSubmit={onSubmit} className="flex flex-col gap-2">
                <label htmlFor="server-url" className="text-sm font-medium">
                  Server URL
                </label>
                <Input
                  id="server-url"
                  value={url}
                  onChange={(e) => {
                    setUrl(e.target.value);
                    setError(null);
                  }}
                  placeholder="https://calendium.your-domain.com"
                  autoComplete="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  disabled={busy !== null}
                />
                {error && <p className="text-sm text-destructive">{error}</p>}
                <Button type="submit" className="mt-1 gap-2" disabled={busy !== null}>
                  {busy === 'server' ? <Loader2 className="animate-spin" /> : <Server />}
                  Connect
                </Button>
              </form>

              <div className="flex items-center gap-3">
                <div className="h-px flex-1 bg-border" />
                <span className="text-[11px] uppercase tracking-wide text-muted-foreground">or</span>
                <div className="h-px flex-1 bg-border" />
              </div>

              <Button
                variant="outline"
                className="gap-2"
                disabled={busy !== null}
                onClick={() => connect(CLOUD_PRESET.serverUrl, 'cloud')}
              >
                {busy === 'cloud' ? <Loader2 className="animate-spin" /> : <Cloud />}
                Use Calendium Cloud
              </Button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
