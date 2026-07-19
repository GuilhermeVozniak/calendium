import type { Metadata } from 'next';

import { SharedThreadView } from '@/components/share/shared-thread-view';

// Static metadata on purpose: resolving the share server-side would put the
// secret token through an extra fetch for no benefit — the client component
// owns the (auth-aware) fetch, and unknown tokens render its inactive state.
export const metadata: Metadata = {
  title: 'Shared conversation',
  description: 'A live, read-only view of an email conversation shared from Calendium.',
};

/**
 * Public shared-conversation route (no (app) auth shell): /shared/{token}.
 * External-audience links work signed out; team-audience links use the
 * viewer's session when present.
 */
export default async function SharedThreadPage({
  params,
}: {
  params: Promise<{ token: string }>;
}) {
  const { token } = await params;
  return <SharedThreadView token={token} />;
}
