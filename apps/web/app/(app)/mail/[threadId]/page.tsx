import { redirect } from 'next/navigation';

/**
 * The mail client uses a Superhuman-style split pane on /mail (?t=<threadId>)
 * rather than a standalone thread route. Keep /mail/<threadId> deep links
 * working by redirecting into the pane.
 */
export default async function ThreadRedirectPage({
  params,
}: {
  params: Promise<{ threadId: string }>;
}) {
  const { threadId } = await params;
  redirect(`/mail?t=${encodeURIComponent(threadId)}`);
}
