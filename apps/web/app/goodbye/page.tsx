import type { Metadata } from 'next';
import Link from 'next/link';

import { AuthShell } from '@/components/auth/auth-shell';
import { Button } from '@/components/ui/button';

export const metadata: Metadata = {
  title: 'Account deleted',
  robots: { index: false },
};

/**
 * Where DeleteAccountDialog lands after Better Auth's deleteUser succeeds:
 * public, outside the session-gated (app) layout, and needs no session. It
 * renders dynamically like every middleware-matched route (the nonce CSP's
 * inline scripts need the per-request nonce), so no `force-static` here.
 */
export default function GoodbyePage() {
  return (
    <AuthShell
      title="Your account has been deleted"
      description="Your Calendium data is gone from our servers. Your mail and calendars stay with Google or Microsoft; you can revoke Calendium's access from their account settings. Thanks for trying Calendium.">
      <Button asChild variant="outline">
        <Link href="/">Back to the home page</Link>
      </Button>
    </AuthShell>
  );
}
