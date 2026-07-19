import type { Metadata } from 'next';
import { WifiOff } from 'lucide-react';

export const metadata: Metadata = {
  title: 'Offline',
};

// Statically prerendered offline shell. The service worker (public/sw.js)
// pre-caches this route at install and serves it as the fallback when a
// navigation fails while offline.
export const dynamic = 'force-static';

export default function OfflinePage() {
  return (
    <div className="bg-background flex min-h-svh flex-col items-center justify-center gap-4 px-6 text-center">
      <div className="bg-muted text-muted-foreground flex size-12 items-center justify-center rounded-xl">
        <WifiOff className="size-6" />
      </div>
      <h1 className="text-lg font-semibold tracking-tight">You’re offline</h1>
      <p className="text-muted-foreground max-w-sm text-sm">
        Calendium can’t reach the network right now. Check your connection and try again — your
        cached mail and calendar are still available from pages you’ve already opened.
      </p>
    </div>
  );
}
