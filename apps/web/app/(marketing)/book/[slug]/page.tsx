import type { Metadata } from 'next';
import { notFound } from 'next/navigation';

import { ApiRequestError, fetchPublicBookingPage } from '@calendium/shared';

import { PublicBookingPage } from '@/components/public/booking-page';
import { env } from '@/lib/env';

interface RouteParams {
  params: Promise<{ slug: string }>;
}

/**
 * Booking pages need their own title/description per link, and unknown slugs
 * should 404 before any markup ships — both are handled here since
 * `generateMetadata` runs ahead of the page body and may call `notFound()`
 * itself. The client component (`PublicBookingPage`) does its own fetch of
 * the same document for interactive state (loading/error/TZ-aware render);
 * Next's fetch request memoization dedupes the two same-URL GETs within a
 * single request.
 */
export async function generateMetadata({ params }: RouteParams): Promise<Metadata> {
  const { slug } = await params;
  try {
    const page = await fetchPublicBookingPage(env.apiUrl, slug);
    return {
      title: `${page.title} — Book with ${page.ownerName}`,
      description:
        page.description ??
        `Book a ${page.durationMinutes}-minute meeting with ${page.ownerName}.`,
    };
  } catch (err) {
    if (err instanceof ApiRequestError && err.status === 404) {
      // Calling notFound() from generateMetadata skips rendering the segment
      // entirely (real 404 status, no client render), which is why the check
      // lives here rather than only in the client component below.
      notFound();
    }
    return { title: 'Book a meeting' };
  }
}

export default async function BookSlugPage({ params }: RouteParams) {
  const { slug } = await params;
  return <PublicBookingPage slug={slug} />;
}
