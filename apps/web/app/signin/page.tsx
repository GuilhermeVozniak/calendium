'use client';

import * as React from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { CalendarRange, Loader2, MailCheck, Zap } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Kbd } from '@/components/ui/kbd';
import { Label } from '@/components/ui/label';
import { Separator } from '@/components/ui/separator';
import { authClient, signIn, signUp } from '@/lib/auth-client';
import { RESEND_COOLDOWN_SECONDS, captureRetryAfter, describeAuthError } from '@/lib/auth-copy';
import { PASSWORD_MIN_LENGTH, passwordPolicyError } from '@/lib/auth-env';
import { useInstance } from '@/lib/use-instance';
import { cn } from '@/lib/utils';

/**
 * Post-sign-in destination from `?next=`, restricted to same-site relative
 * paths (no protocol-relative `//` — prevents open redirects). Defaults to the
 * inbox. Read from the live URL so this stays a pure client page (no Suspense
 * boundary needed, unlike useSearchParams).
 */
function nextDestination(): string {
  if (typeof window === 'undefined') return '/mail';
  const next = new URLSearchParams(window.location.search).get('next');
  if (next?.startsWith('/') && !next.startsWith('//')) return next;
  return '/mail';
}

const PROVIDER_LABELS: Record<SocialProvider, string> = {
  google: 'Google',
  apple: 'Apple',
};

function GoogleIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" {...props}>
      <path
        fill="#4285F4"
        d="M23.49 12.27c0-.79-.07-1.54-.19-2.27H12v4.51h6.47a5.53 5.53 0 0 1-2.4 3.58v3h3.86c2.26-2.09 3.56-5.17 3.56-8.82Z"
      />
      <path
        fill="#34A853"
        d="M12 24c3.24 0 5.95-1.08 7.93-2.91l-3.86-3c-1.08.72-2.45 1.16-4.07 1.16-3.13 0-5.78-2.11-6.73-4.96H1.29v3.09A11.99 11.99 0 0 0 12 24Z"
      />
      <path
        fill="#FBBC05"
        d="M5.27 14.29A7.19 7.19 0 0 1 4.89 12c0-.8.14-1.57.38-2.29V6.62H1.29a12.04 12.04 0 0 0 0 10.76l3.98-3.09Z"
      />
      <path
        fill="#EA4335"
        d="M12 4.75c1.77 0 3.35.61 4.6 1.8l3.42-3.42C17.95 1.19 15.24 0 12 0 7.31 0 3.26 2.69 1.29 6.62l3.98 3.09C6.22 6.86 8.87 4.75 12 4.75Z"
      />
    </svg>
  );
}

function AppleIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" {...props}>
      <path d="M16.36 12.79c.03 3.26 2.86 4.35 2.89 4.36-.02.08-.45 1.55-1.49 3.07-.9 1.31-1.83 2.62-3.3 2.65-1.44.03-1.91-.86-3.56-.86-1.65 0-2.17.83-3.53.89-1.42.05-2.5-1.42-3.4-2.73C2.1 17.5.7 12.62 2.6 9.39a5.27 5.27 0 0 1 4.45-2.7c1.39-.03 2.7.94 3.56.94.85 0 2.45-1.16 4.13-.99.7.03 2.68.28 3.94 2.14-.1.06-2.35 1.37-2.32 4.01ZM13.63 4.87c.75-.91 1.26-2.17 1.12-3.43-1.08.04-2.39.72-3.17 1.63-.7.8-1.31 2.09-1.14 3.32 1.2.09 2.44-.61 3.19-1.52Z" />
    </svg>
  );
}

type SocialProvider = 'google' | 'apple';
type Pending = SocialProvider | 'email' | null;

type Mode = 'signin' | 'signup' | 'check-email';

/** Second-based countdown; returns the remaining seconds and a restart function. */
function useCooldown(seconds: number): [number, () => void] {
  const [remaining, setRemaining] = React.useState(0);
  React.useEffect(() => {
    if (remaining <= 0) return;
    const id = window.setInterval(() => setRemaining((r) => (r > 0 ? r - 1 : 0)), 1000);
    return () => window.clearInterval(id);
  }, [remaining]);
  const restart = React.useCallback(() => setRemaining(seconds), [seconds]);
  return [remaining, restart];
}

export default function SignInPage() {
  const router = useRouter();
  const [mode, setMode] = React.useState<Mode>('signin');
  const [pending, setPending] = React.useState<Pending>(null);
  const [name, setName] = React.useState('');
  const [email, setEmail] = React.useState('');
  const [password, setPassword] = React.useState('');
  // check-email state: which address we told the user to look at, and why.
  const [checkEmail, setCheckEmail] = React.useState('');
  const [notice, setNotice] = React.useState<string | null>(null);
  const [cooldown, restartCooldown] = useCooldown(RESEND_COOLDOWN_SECONDS);

  // Only offer the social providers the server actually advertises (a
  // self-host without Google/Apple creds omits them from /v1/instance).
  const { data: instance } = useInstance();
  const providers = instance?.authProviders ?? [];
  const showGoogle = providers.includes('google');
  const showApple = providers.includes('apple');
  const showSocial = showGoogle || showApple;

  // Already signed in → honor ?next=, else straight to the inbox.
  const { data: session } = authClient.useSession();
  React.useEffect(() => {
    if (session) router.replace(nextDestination());
  }, [session, router]);

  function showCheckEmail(address: string, message: string | null) {
    setCheckEmail(address);
    setNotice(message);
    setMode('check-email');
    restartCooldown();
  }

  async function social(provider: SocialProvider) {
    if (!providers.includes(provider)) {
      toast.error(`${PROVIDER_LABELS[provider]} sign-in isn't enabled on this server.`);
      return;
    }
    setPending(provider);
    try {
      const { error } = await signIn.social({ provider, callbackURL: nextDestination() });
      if (error) throw new Error(error.message ?? 'Sign-in failed');
      // Success → the browser is being redirected to the provider.
    } catch (err) {
      setPending(null);
      toast.error(err instanceof Error ? err.message : 'Could not start sign-in. Try again.');
    }
  }

  async function submitEmail(e: React.FormEvent) {
    e.preventDefault();
    if (mode === 'signup') {
      const violation = passwordPolicyError(password, email);
      if (violation) {
        toast.error(violation.message);
        return;
      }
    }
    setPending('email');
    const retry = captureRetryAfter();
    try {
      if (mode === 'signup') {
        const { data, error } = await signUp.email(
          { name: name || email, email, password, callbackURL: '/verify-email' },
          retry.fetchOptions
        );
        if (error) {
          toast.error(describeAuthError(error, { fallback: 'Could not create account', retryAfter: retry.value, sendsMail: true }));
          return;
        }
        // With SMTP configured the server never signs a new account in:
        // `token === null` for a new address AND for an existing one (no
        // enumeration), so both land on the same "check your inbox" screen.
        if (data && data.token === null) {
          showCheckEmail(email, null);
          return;
        }
      } else {
        const { error } = await signIn.email({ email, password, callbackURL: nextDestination() }, retry.fetchOptions);
        if (error) {
          if (error.code === 'EMAIL_NOT_VERIFIED') {
            // The server re-sent the verification link (sendOnSignIn).
            showCheckEmail(email, describeAuthError(error, { fallback: 'Invalid email or password' }));
            return;
          }
          toast.error(describeAuthError(error, { fallback: 'Invalid email or password', retryAfter: retry.value }));
          return;
        }
      }
      router.replace(nextDestination());
    } finally {
      setPending(null);
    }
  }

  async function resend() {
    setPending('email');
    const retry = captureRetryAfter();
    try {
      const { error } = await authClient.sendVerificationEmail({ email: checkEmail, callbackURL: '/verify-email' }, retry.fetchOptions);
      if (error) {
        toast.error(describeAuthError(error, { fallback: 'Could not resend the email', retryAfter: retry.value, sendsMail: true }));
        return;
      }
      restartCooldown();
    } finally {
      setPending(null);
    }
  }

  const busy = pending !== null;

  return (
    <main className="relative flex min-h-svh flex-col items-center justify-center overflow-hidden px-6">
      {/* Faint dot grid backdrop */}
      <div
        aria-hidden
        className="absolute inset-0 -z-10 [background-image:radial-gradient(hsl(var(--border))_1px,transparent_1px)] [mask-image:radial-gradient(ellipse_60%_50%_at_50%_45%,black,transparent)] [background-size:24px_24px]"
      />

      <div className="flex w-full max-w-sm flex-col items-center">
        <div className="bg-primary text-primary-foreground flex size-12 items-center justify-center rounded-xl shadow-sm">
          <CalendarRange className="size-6" />
        </div>

        {mode === 'check-email' ? (
          <>
            <h1 className="mt-6 text-2xl font-semibold tracking-tight">Check your inbox</h1>
            <div className="text-muted-foreground mt-2 flex flex-col gap-1 text-center text-sm text-balance" role="status">
              {notice && <p className="text-foreground">{notice}</p>}
              <p>
                {notice ? 'We sent it to' : 'If that address is new to Calendium, we sent a verification link to'}{' '}
                <span className="text-foreground font-medium">{checkEmail}</span>. Open it to continue.
              </p>
            </div>
            <MailCheck className="text-muted-foreground mt-8 size-8" aria-hidden />
            <Button
              variant="outline"
              size="lg"
              className="mt-6 w-full"
              disabled={busy || cooldown > 0}
              onClick={() => void resend()}>
              {pending === 'email' ? <Loader2 className="animate-spin" /> : null}
              {cooldown > 0 ? `Resend in ${cooldown} s` : 'Resend email'}
            </Button>
            <button
              type="button"
              className="text-muted-foreground hover:text-foreground mt-4 text-xs transition-colors"
              onClick={() => setMode('signin')}
              disabled={busy}>
              Back to sign in
            </button>
          </>
        ) : (
          <>
            <h1 className="mt-6 text-2xl font-semibold tracking-tight">Calendium</h1>
            <p className="text-muted-foreground mt-2 text-center text-sm text-balance">
              The fastest email and calendar experience. One inbox, one calendar, zero friction.
            </p>

            {showSocial && (
              <>
                <div className="mt-8 flex w-full flex-col gap-2.5">
                  {showGoogle && (
                    <Button variant="outline" size="lg" className="w-full" disabled={busy} onClick={() => social('google')}>
                      {pending === 'google' ? <Loader2 className="animate-spin" /> : <GoogleIcon className="size-4" />}
                      Continue with Google
                    </Button>
                  )}
                  {showApple && (
                    <Button variant="outline" size="lg" className="w-full" disabled={busy} onClick={() => social('apple')}>
                      {pending === 'apple' ? <Loader2 className="animate-spin" /> : <AppleIcon className="size-4" />}
                      Continue with Apple
                    </Button>
                  )}
                </div>

                <div className="mt-6 flex w-full items-center gap-3">
                  <Separator className="flex-1" />
                  <span className="text-muted-foreground text-xs">or with email</span>
                  <Separator className="flex-1" />
                </div>
              </>
            )}

            <form onSubmit={submitEmail} className={cn('flex w-full flex-col gap-3', showSocial ? 'mt-6' : 'mt-8')}>
              {mode === 'signup' && (
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="name">Name</Label>
                  <Input id="name" type="text" autoComplete="name" placeholder="Ada Lovelace" value={name} onChange={(e) => setName(e.target.value)} disabled={busy} />
                </div>
              )}
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="email">Email</Label>
                <Input id="email" type="email" autoComplete="email" required placeholder="you@calendium.app" value={email} onChange={(e) => setEmail(e.target.value)} disabled={busy} />
              </div>
              <div className="flex flex-col gap-1.5">
                <div className="flex items-center justify-between">
                  <Label htmlFor="password">Password</Label>
                  {mode === 'signin' && (
                    <Link href="/forgot-password" className="text-muted-foreground hover:text-foreground text-xs underline-offset-2 hover:underline">
                      Forgot password?
                    </Link>
                  )}
                </div>
                <Input
                  id="password"
                  type="password"
                  autoComplete={mode === 'signup' ? 'new-password' : 'current-password'}
                  required
                  minLength={PASSWORD_MIN_LENGTH}
                  placeholder="••••••••••"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  disabled={busy}
                />
                {mode === 'signup' && (
                  <p className="text-muted-foreground text-xs">At least {PASSWORD_MIN_LENGTH} characters, and not your email address.</p>
                )}
              </div>
              <Button type="submit" size="lg" className="w-full" disabled={busy}>
                {pending === 'email' ? <Loader2 className="animate-spin" /> : null}
                {mode === 'signup' ? 'Create account' : 'Sign in'}
              </Button>
            </form>

            <button
              type="button"
              className="text-muted-foreground hover:text-foreground mt-4 text-xs transition-colors"
              onClick={() => setMode((m) => (m === 'signin' ? 'signup' : 'signin'))}
              disabled={busy}>
              {mode === 'signin' ? "Don't have an account? Sign up" : 'Already have an account? Sign in'}
            </button>

            <div className="text-muted-foreground mt-6 flex items-center gap-2 text-xs">
              <Zap className="size-3.5" />
              <span>
                Every action is a keystroke away — hit <Kbd size="sm">⌘</Kbd> <Kbd size="sm">K</Kbd> once you're in.
              </span>
            </div>

            <p className="text-muted-foreground mt-8 text-center text-xs text-balance">
              {instance?.features.billing ? '14-day free trial, then $50/year. ' : ''}By continuing you agree to the{' '}
              <Link href="/terms" className="hover:text-foreground underline underline-offset-2">Terms</Link> and{' '}
              <Link href="/privacy" className="hover:text-foreground underline underline-offset-2">Privacy Policy</Link>.
            </p>
          </>
        )}
      </div>
    </main>
  );
}
