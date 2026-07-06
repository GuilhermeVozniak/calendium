import Link from 'next/link';

import { Button } from '@/components/ui/button';
import { APP_HREF, START_TRIAL_HREF } from './links';
import { Wordmark } from './wordmark';

const navLinks = [
  { href: '/#features', label: 'Features' },
  { href: '/pricing', label: 'Pricing' },
];

export function SiteHeader() {
  return (
    <header className="sticky top-0 z-50 border-b border-border/60 bg-background/80 backdrop-blur supports-[backdrop-filter]:bg-background/60">
      <div className="mx-auto flex h-14 w-full max-w-6xl items-center justify-between px-6">
        <div className="flex items-center gap-10">
          <Wordmark />
          <nav aria-label="Main" className="hidden items-center gap-7 sm:flex">
            {navLinks.map((link) => (
              <Link
                key={link.href}
                href={link.href}
                className="text-sm text-muted-foreground transition-colors hover:text-foreground">
                {link.label}
              </Link>
            ))}
          </nav>
        </div>
        <div className="flex items-center gap-2">
          <Button asChild variant="ghost" size="sm" className="hidden text-muted-foreground sm:inline-flex">
            <Link href={APP_HREF}>Open app</Link>
          </Button>
          <Button asChild size="sm">
            <Link href={START_TRIAL_HREF}>Start free trial</Link>
          </Button>
        </div>
      </div>
    </header>
  );
}
