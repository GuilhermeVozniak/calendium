import Link from 'next/link';

import { APP_HREF, SUPPORT_EMAIL } from './links';
import { Wordmark } from './wordmark';

const columns: { title: string; links: { label: string; href: string }[] }[] = [
  {
    title: 'Product',
    links: [
      { label: 'Features', href: '/#features' },
      { label: 'Pricing', href: '/pricing' },
      { label: 'Open app', href: APP_HREF },
    ],
  },
  {
    title: 'Company',
    links: [
      { label: 'Support', href: `mailto:${SUPPORT_EMAIL}` },
      { label: 'Contact', href: `mailto:${SUPPORT_EMAIL}` },
    ],
  },
  {
    title: 'Legal',
    links: [
      { label: 'Privacy', href: '/privacy' },
      { label: 'Terms', href: '/terms' },
    ],
  },
];

/** "© <year> Calendium" — computed at render so it is never stale. */
export function copyrightLine(year: number = new Date().getFullYear()): string {
  return `© ${year} Calendium`;
}

export function SiteFooter() {
  return (
    <footer className="border-t">
      <div className="mx-auto w-full max-w-6xl px-6 py-14">
        <div className="flex flex-col justify-between gap-10 md:flex-row">
          <div className="max-w-xs space-y-3">
            <Wordmark />
            <p className="text-sm leading-relaxed text-muted-foreground">
              Email and calendar, at the speed of thought. One subscription, every platform.
            </p>
          </div>
          <div className="grid grid-cols-2 gap-10 sm:grid-cols-3">
            {columns.map((column) => (
              <div key={column.title} className="space-y-3">
                <p className="font-mono text-xs uppercase tracking-[0.18em] text-muted-foreground">
                  {column.title}
                </p>
                <ul className="space-y-2">
                  {column.links.map((link) => (
                    <li key={link.label}>
                      <Link
                        href={link.href}
                        className="text-sm text-muted-foreground transition-colors hover:text-foreground">
                        {link.label}
                      </Link>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </div>
        <div className="mt-12 flex items-center justify-between border-t pt-6">
          <p className="text-xs text-muted-foreground">{copyrightLine()}</p>
          <p className="font-mono text-xs text-muted-foreground">42 ms, always.</p>
        </div>
      </div>
    </footer>
  );
}
