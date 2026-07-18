import type { Metadata } from 'next';

import { PublicPollPage } from '@/components/public/poll-page';

export const metadata: Metadata = {
  title: 'Meeting poll',
  description: 'Vote on a proposed meeting time.',
};

export default async function PollTokenPage({
  params,
}: {
  params: Promise<{ token: string }>;
}) {
  const { token } = await params;
  return <PublicPollPage token={token} />;
}
