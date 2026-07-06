import { CalendarClock, Newspaper, Users } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Kbd } from '@/components/ui/kbd';
import { SectionHeading } from './section-heading';

const incoming: { sender: string; subject: string; split: string; filled?: boolean }[] = [
  { sender: 'Maya Chen', subject: 'Q3 board deck', split: 'Important', filled: true },
  { sender: 'Ari Gold', subject: 'Intro — Meridian founders', split: 'VIP', filled: true },
  { sender: 'Google Calendar', subject: 'Invite: Roadmap review, Thu 13:00', split: 'Calendar' },
  { sender: 'Daniel Ruiz', subject: 'Deploy notes — week 27', split: 'Team' },
  { sender: 'The Pragmatic Engineer', subject: 'The state of dev tooling', split: 'News' },
  { sender: 'LinkedIn', subject: 'You appeared in 12 searches', split: 'Social' },
];

const points = [
  {
    icon: Users,
    title: 'VIPs surface first',
    copy: 'Mark the people who matter; their mail always lands on top, on every device.',
  },
  {
    icon: CalendarClock,
    title: 'Invites become events',
    copy: 'Calendar mail turns into RSVP cards you answer inline — no tab-switching.',
  },
  {
    icon: Newspaper,
    title: 'News, batched',
    copy: 'Newsletters, receipts, and notifications wait quietly until you ask for them.',
  },
];

export function TriageSection() {
  return (
    <section className="border-t">
      <div className="mx-auto grid w-full max-w-6xl items-center gap-12 px-6 py-24 md:py-32 lg:grid-cols-[1.15fr_1fr] lg:gap-20">
        {/* Mock: incoming mail auto-filed into splits */}
        <div aria-hidden className="order-last lg:order-first">
          <div className="overflow-hidden rounded-xl border bg-card shadow-sm">
            <div className="flex items-center justify-between border-b px-4 py-2.5">
              <span className="text-[11px] font-medium text-muted-foreground">
                Incoming — filed on arrival
              </span>
              <span className="font-mono text-[10px] text-muted-foreground">0 rules written</span>
            </div>
            <div className="divide-y divide-border/60">
              {incoming.map((mail) => (
                <div key={mail.subject} className="flex items-center gap-3 px-4 py-2.5">
                  <span className="w-28 shrink-0 truncate text-[11px] font-medium sm:w-36">
                    {mail.sender}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground">
                    {mail.subject}
                  </span>
                  <Badge
                    variant={mail.filled ? 'default' : 'secondary'}
                    className="shrink-0 px-1.5 text-[10px]">
                    {mail.split}
                  </Badge>
                </div>
              ))}
            </div>
            <div className="flex items-center gap-1.5 border-t px-4 py-2 text-[10px] text-muted-foreground">
              <Kbd size="sm">Tab</Kbd>
              moves between splits · classified at ingest, refined by AI
            </div>
          </div>
        </div>

        <div>
          <SectionHeading
            eyebrow="Split inbox"
            keys={['Tab']}
            title="Triage, not inbox archaeology."
            lede="Mail is classified as it arrives — the important things in front, everything else folded away into splits. Two keys clear each one."
          />
          <ul className="mt-10 space-y-6">
            {points.map((point) => (
              <li key={point.title} className="flex gap-4">
                <span className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-md border bg-muted/40">
                  <point.icon className="size-4" strokeWidth={1.75} />
                </span>
                <div>
                  <p className="text-sm font-medium">{point.title}</p>
                  <p className="mt-1 text-sm leading-relaxed text-muted-foreground">{point.copy}</p>
                </div>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </section>
  );
}
