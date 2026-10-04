import { Loader2 } from 'lucide-react';
import { type FormEvent, useEffect, useState } from 'react';

import { signInEmail, signUpEmail, verifyOtt } from '@/lib/auth';
import { forgotPasswordUrl, useServerConfig, webOrigin } from '@/lib/server-config';
import { desktop, onDeepLink } from '@/lib/wails';
import { Button } from '@/ui/button';
import { Input } from '@/ui/input';

/** Google "G" mark (brand colors), inline so we ship no external asset. */
function GoogleIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" className="size-4">
      <path
        fill="#4285F4"
        d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 0 1-2.2 3.32v2.76h3.56c2.08-1.92 3.28-4.74 3.28-8.09Z"
      />
      <path
        fill="#34A853"
        d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.56-2.76c-.98.66-2.23 1.06-3.72 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84A11 11 0 0 0 12 23Z"
      />
      <path
        fill="#FBBC05"
        d="M5.84 14.1a6.6 6.6 0 0 1 0-4.2V7.06H2.18a11 11 0 0 0 0 9.88l3.66-2.84Z"
      />
      <path
        fill="#EA4335"
        d="M12 5.38c1.62 0 3.06.56 4.2 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1A11 11 0 0 0 2.18 7.06l3.66 2.84C6.71 7.3 9.14 5.38 12 5.38Z"
      />
    </svg>
  );
}

/** Apple logo, inline, tinted with the current text color. */
function AppleIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" className="size-4" fill="currentColor">
      <path d="M16.36 12.9c-.02-2.4 1.96-3.55 2.05-3.6-1.12-1.64-2.86-1.86-3.48-1.89-1.48-.15-2.89.87-3.64.87-.75 0-1.91-.85-3.14-.83-1.62.02-3.11.94-3.94 2.39-1.68 2.92-.43 7.24 1.2 9.61.8 1.16 1.75 2.46 3 2.41 1.2-.05 1.66-.78 3.11-.78 1.45 0 1.86.78 3.14.75 1.3-.02 2.12-1.18 2.91-2.35.92-1.35 1.3-2.65 1.32-2.72-.03-.01-2.53-.97-2.55-3.85ZM14.2 5.36c.66-.8 1.1-1.92.98-3.03-.95.04-2.1.63-2.78 1.43-.61.71-1.14 1.85-1 2.94 1.06.08 2.14-.54 2.8-1.34Z" />
    </svg>
  );
}

/**
 * Sign-in gate (Better Auth): shown after a server is configured but before the
 * user has a session. Email + password runs in-process. Google/Apple can't run
 * in an embedded WebView, so those open the web sign-in in the system browser
 * (contract item 11); it hands a one-time token back via the
 * calendium://auth/callback?ott=... deep link (or a pasted code) which we redeem
 * with verifyOtt. On success the session store flips and the app mounts.
 */
export function SignInView() {
  const { config } = useServerConfig();
  const providers = config?.authProviders ?? ['email'];
  const showGoogle = providers.includes('google');
  const showApple = providers.includes('apple');

  const [mode, setMode] = useState<'signin' | 'signup'>('signin');
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState<null | 'email'>(null);
  const [socialStarted, setSocialStarted] = useState(false);
  const [code, setCode] = useState('');
  const [verifying, setVerifying] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  async function handleVerify(token: string) {
    setError(null);
    setVerifying(true);
    const res = await verifyOtt(token);
    // On success the session store flips and the gate swaps to <App/>; on
    // failure surface the message and re-enable the input.
    if (!res.ok) {
      setError(res.error ?? 'Could not verify that code.');
      setVerifying(false);
    }
  }

  // Desktop deep-link handoff: the browser returns calendium://auth/callback?ott=…
  useEffect(
    () =>
      onDeepLink((url) => {
        if (!url.startsWith('calendium://auth')) return;
        let ott = '';
        try {
          ott = new URL(url).searchParams.get('ott') ?? '';
        } catch {
          // Ignore malformed deep links.
        }
        if (ott) void handleVerify(ott);
      }),
    []
  );

  async function submitEmail(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setNotice(null);
    setBusy('email');
    const res =
      mode === 'signin'
        ? await signInEmail(email, password)
        : await signUpEmail(name, email, password);
    if (!res.ok) {
      setError(res.error ?? 'Something went wrong.');
      setBusy(null);
      return;
    }
    if (res.verificationRequired) {
      // No session yet: the server emailed a verification link. Flip to
      // sign-in so the user can continue once the link is clicked.
      setNotice(`Check your inbox — we sent a verification link to ${email}.`);
      setMode('signin');
      setPassword('');
      setBusy(null);
    }
  }

  function openForgotPassword() {
    const url = forgotPasswordUrl(config);
    if (!url) {
      setError('Connect to a server first.');
      return;
    }
    desktop.OpenExternal(url);
  }

  function openWebSignIn() {
    const origin = webOrigin(config);
    if (!origin) {
      setError('Connect to a server first.');
      return;
    }
    setError(null);
    setSocialStarted(true);
    desktop.OpenExternal(`${origin}/signin?next=/desktop-callback`);
  }

  const isSignup = mode === 'signup';

  return (
    <div className="flex h-full flex-col overflow-hidden bg-background">
      {/* Draggable strip so the frameless window stays movable. */}
      <header className="titlebar-drag h-10 shrink-0" />
      <div className="flex min-h-0 flex-1 items-center justify-center p-6">
        <div className="w-full max-w-sm">
          <div className="mb-8 flex flex-col items-center gap-3 text-center">
            <div className="flex size-14 items-center justify-center rounded-2xl bg-primary text-lg font-semibold text-primary-foreground">
              C
            </div>
            <div>
              <h1 className="text-lg font-semibold tracking-tight">
                {isSignup ? 'Create your account' : 'Sign in to Calendium'}
              </h1>
              <p className="mt-1 text-sm text-muted-foreground">{config?.name ?? 'Calendium'}</p>
            </div>
          </div>

          <div className="flex flex-col gap-4">
            {(showGoogle || showApple) && (
              <>
                <div className="flex flex-col gap-2">
                  {showGoogle && (
                    <Button
                      variant="outline"
                      className="gap-2"
                      disabled={verifying}
                      onClick={openWebSignIn}
                    >
                      <GoogleIcon />
                      Continue with Google
                    </Button>
                  )}
                  {showApple && (
                    <Button
                      variant="outline"
                      className="gap-2"
                      disabled={verifying}
                      onClick={openWebSignIn}
                    >
                      <AppleIcon />
                      Continue with Apple
                    </Button>
                  )}
                </div>

                {socialStarted && (
                  <p className="text-xs text-muted-foreground">
                    Finish signing in in your browser, then return to Calendium. If it doesn't
                    switch back on its own, paste the code from your browser below.
                  </p>
                )}

                <div className="flex items-center gap-2">
                  <Input
                    aria-label="Sign-in code"
                    value={code}
                    onChange={(e) => {
                      setCode(e.target.value);
                      setError(null);
                    }}
                    placeholder="Have a code? Paste it here"
                    autoComplete="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    disabled={verifying}
                  />
                  <Button
                    variant="outline"
                    disabled={verifying || !code.trim()}
                    onClick={() => void handleVerify(code)}
                  >
                    {verifying ? <Loader2 className="animate-spin" /> : null}
                    Verify
                  </Button>
                </div>

                <div className="flex items-center gap-3">
                  <div className="h-px flex-1 bg-border" />
                  <span className="text-[11px] uppercase tracking-wide text-muted-foreground">
                    or
                  </span>
                  <div className="h-px flex-1 bg-border" />
                </div>
              </>
            )}

            <form onSubmit={submitEmail} className="flex flex-col gap-2">
              {isSignup && (
                <Input
                  aria-label="Name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Name"
                  autoComplete="name"
                  disabled={busy !== null}
                />
              )}
              <Input
                aria-label="Email"
                type="email"
                value={email}
                onChange={(e) => {
                  setEmail(e.target.value);
                  setError(null);
                }}
                placeholder="you@example.com"
                autoComplete="email"
                autoCapitalize="none"
                spellCheck={false}
                required
                disabled={busy !== null}
              />
              <Input
                aria-label="Password"
                type="password"
                value={password}
                onChange={(e) => {
                  setPassword(e.target.value);
                  setError(null);
                }}
                placeholder="Password"
                autoComplete={isSignup ? 'new-password' : 'current-password'}
                required
                disabled={busy !== null}
              />
              {error && <p className="text-sm text-destructive">{error}</p>}
              {notice && (
                <p className="text-sm text-muted-foreground" role="status">
                  {notice}
                </p>
              )}
              {!isSignup && (
                <button
                  type="button"
                  className="self-end text-xs text-muted-foreground underline-offset-4 hover:underline"
                  onClick={openForgotPassword}
                  disabled={busy !== null}
                >
                  Forgot password?
                </button>
              )}
              <Button type="submit" className="mt-1 gap-2" disabled={busy !== null}>
                {busy === 'email' && <Loader2 className="animate-spin" />}
                {isSignup ? 'Create account' : 'Sign in'}
              </Button>
            </form>

            <p className="text-center text-xs text-muted-foreground">
              {isSignup ? 'Already have an account?' : "Don't have an account?"}{' '}
              <button
                type="button"
                className="font-medium text-foreground underline-offset-4 hover:underline"
                onClick={() => {
                  setMode(isSignup ? 'signin' : 'signup');
                  setError(null);
                }}
              >
                {isSignup ? 'Sign in' : 'Create one'}
              </button>
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
