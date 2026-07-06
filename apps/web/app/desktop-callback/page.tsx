'use client';

import * as React from 'react';
import { useRouter } from 'next/navigation';
import { Check, Copy, Loader2, MonitorSmartphone } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { authClient } from '@/lib/auth-client';

/**
 * Desktop sign-in handoff. The desktop app opens `${webOrigin}/signin?next=/desktop-callback`
 * in the system browser; after the user authenticates here (email or social),
 * this page mints a short-lived one-time token and hands it to the app:
 *   1. deep link to `calendium://auth/callback?ott=<token>` (auto-return), and
 *   2. a copy-able code fallback the user can paste into the app.
 * The desktop app verifies the token at `{authBaseUrl}/one-time-token/verify`
 * and stores the returned session token like an email/password sign-in.
 */
const DEEP_LINK = 'calendium://auth/callback';

export default function DesktopCallbackPage() {
  const router = useRouter();
  const { data: session, isPending } = authClient.useSession();
  const [token, setToken] = React.useState<string | null>(null);
  const [status, setStatus] = React.useState<'loading' | 'ready' | 'error'>('loading');
  const [copied, setCopied] = React.useState(false);

  // Not signed in → sign in first, then return here.
  React.useEffect(() => {
    if (!isPending && !session) {
      router.replace('/signin?next=/desktop-callback');
    }
  }, [isPending, session, router]);

  // Signed in → mint a one-time token and hand it to the desktop app.
  React.useEffect(() => {
    if (!session || token) return;
    let active = true;
    void (async () => {
      const { data, error } = await authClient.oneTimeToken.generate();
      if (!active) return;
      if (error || !data?.token) {
        setStatus('error');
        return;
      }
      setToken(data.token);
      setStatus('ready');
      // Hand off to the desktop app via its custom scheme. Best-effort — if the
      // OS has no handler registered, the copy-able code below is the fallback.
      window.location.href = `${DEEP_LINK}?ott=${encodeURIComponent(data.token)}`;
    })();
    return () => {
      active = false;
    };
  }, [session, token]);

  async function copyCode() {
    if (!token) return;
    try {
      await navigator.clipboard.writeText(token);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard blocked (permissions) — the code is still visible to type.
    }
  }

  return (
    <main className="flex min-h-svh flex-col items-center justify-center px-6">
      <div className="flex w-full max-w-sm flex-col items-center text-center">
        <div className="bg-primary text-primary-foreground flex size-12 items-center justify-center rounded-xl shadow-sm">
          <MonitorSmartphone className="size-6" />
        </div>

        {status === 'error' ? (
          <>
            <h1 className="mt-6 text-xl font-semibold tracking-tight">Couldn&apos;t finish sign-in</h1>
            <p className="text-muted-foreground mt-2 text-sm text-balance">
              We couldn&apos;t generate a sign-in code. Head back to the desktop app and try again.
            </p>
            <Button className="mt-6" onClick={() => window.location.reload()}>
              Try again
            </Button>
          </>
        ) : status === 'loading' ? (
          <>
            <h1 className="mt-6 text-xl font-semibold tracking-tight">Signing you in…</h1>
            <p className="text-muted-foreground mt-2 flex items-center gap-2 text-sm">
              <Loader2 className="size-4 animate-spin" />
              Preparing your desktop session
            </p>
          </>
        ) : (
          <>
            <h1 className="mt-6 text-xl font-semibold tracking-tight">Return to the app</h1>
            <p className="text-muted-foreground mt-2 text-sm text-balance">
              Calendium should reopen automatically. If it doesn&apos;t, enter this code in the
              desktop app to finish signing in.
            </p>

            <div className="mt-6 flex w-full items-center gap-2">
              <code className="bg-muted flex-1 truncate rounded-md px-3 py-2 text-left font-mono text-xs">
                {token}
              </code>
              <Button variant="outline" size="icon" onClick={copyCode} aria-label="Copy code">
                {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
              </Button>
            </div>

            <p className="text-muted-foreground mt-4 text-xs">
              This code expires in a few minutes and can be used once.
            </p>
          </>
        )}
      </div>
    </main>
  );
}
