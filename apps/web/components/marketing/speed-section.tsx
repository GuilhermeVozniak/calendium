import type { ReactNode } from 'react';

import { Kbd, KbdGroup } from '@/components/ui/kbd';
import { SectionHeading } from './section-heading';

const shortcuts: { label: string; ms: string; keys: ReactNode }[] = [
  { label: 'Archive', ms: '31', keys: <Kbd>E</Kbd> },
  { label: 'Snooze until Monday', ms: '27', keys: <Kbd>H</Kbd> },
  { label: 'Reply', ms: '24', keys: <Kbd>↵</Kbd> },
  { label: 'Command palette', ms: '19', keys: <KbdGroup keys={['⌘', 'K']} /> },
  {
    label: 'Go to calendar',
    ms: '22',
    keys: (
      <span className="inline-flex items-center gap-1.5">
        <Kbd>G</Kbd>
        <span className="font-mono text-[10px] text-muted-foreground">then</span>
        <Kbd>C</Kbd>
      </span>
    ),
  },
  { label: 'Send later', ms: '29', keys: <KbdGroup keys={['⌘', '⇧', 'L']} /> },
  { label: 'Share availability', ms: '35', keys: <KbdGroup keys={['⌘', '⇧', 'A']} /> },
  { label: 'Search everything', ms: '44', keys: <Kbd>/</Kbd> },
];

export function SpeedSection() {
  return (
    <section id="features" className="scroll-mt-20 border-t">
      <div className="mx-auto grid w-full max-w-6xl gap-12 px-6 py-24 md:py-32 lg:grid-cols-[1fr_1.15fr] lg:gap-20">
        <div>
          <SectionHeading
            eyebrow="Speed"
            title="Every action under 100 ms."
            lede="Your mail and calendar are mirrored locally and served from memory. No spinners, no round trips to a server across an ocean — the interface answers at the speed of your fingers."
          />
          <dl className="mt-10 grid max-w-xs gap-2.5 font-mono text-xs text-muted-foreground">
            <div className="flex items-baseline justify-between border-b pb-2.5">
              <dt>p50 interaction</dt>
              <dd className="tabular-nums text-foreground">42 ms</dd>
            </div>
            <div className="flex items-baseline justify-between border-b pb-2.5">
              <dt>p95 interaction</dt>
              <dd className="tabular-nums text-foreground">87 ms</dd>
            </div>
            <div className="flex items-baseline justify-between">
              <dt>sync</dt>
              <dd className="text-foreground">continuous</dd>
            </div>
          </dl>
        </div>

        <div className="self-center">
          <ul className="divide-y border-y">
            {shortcuts.map((shortcut) => (
              <li
                key={shortcut.label}
                className="flex items-center justify-between gap-4 px-1 py-3.5 transition-colors hover:bg-accent/40">
                <span className="text-sm">{shortcut.label}</span>
                <span className="flex items-center gap-4">
                  {shortcut.keys}
                  <span className="w-14 text-right font-mono text-xs tabular-nums text-muted-foreground">
                    {shortcut.ms} ms
                  </span>
                </span>
              </li>
            ))}
          </ul>
          <p className="mt-4 text-right font-mono text-[11px] text-muted-foreground">
            measured tap-to-paint, production build
          </p>
        </div>
      </div>
    </section>
  );
}
