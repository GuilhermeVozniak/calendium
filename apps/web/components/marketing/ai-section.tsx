import { AlignLeft, MessageSquare, PenLine, Sparkles } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { KbdGroup } from '@/components/ui/kbd';
import { SectionHeading } from './section-heading';

const modes = [
  {
    icon: PenLine,
    title: 'Compose & reply',
    copy: 'Give it intent — “polite decline, propose next week” — and get a draft that sounds like you.',
  },
  {
    icon: AlignLeft,
    title: 'Summarize',
    copy: 'A forty-message thread becomes three bullets you can act on.',
  },
  {
    icon: MessageSquare,
    title: 'Ask',
    copy: '“What did legal say about the SOC 2 timeline?” Answered from your own mail, with sources.',
  },
];

export function AiSection() {
  return (
    <section className="border-t">
      <div className="mx-auto grid w-full max-w-6xl items-center gap-12 px-6 py-24 md:py-32 lg:grid-cols-[1fr_1.15fr] lg:gap-20">
        <div>
          <SectionHeading
            eyebrow="AI"
            keys={['⌘', 'J']}
            title="Written for you, in your voice."
            lede="Compose, summarize, and ask across your whole inbox — routed through OpenRouter, invoked with one key, never used to train on your mail."
          />
          <ul className="mt-10 space-y-6">
            {modes.map((mode) => (
              <li key={mode.title} className="flex gap-4">
                <span className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-md border bg-muted/40">
                  <mode.icon className="size-4" strokeWidth={1.75} />
                </span>
                <div>
                  <p className="text-sm font-medium">{mode.title}</p>
                  <p className="mt-1 text-sm leading-relaxed text-muted-foreground">{mode.copy}</p>
                </div>
              </li>
            ))}
          </ul>
        </div>

        {/* Mock: AI compose card */}
        <div aria-hidden>
          <div className="overflow-hidden rounded-xl border bg-card shadow-sm">
            <div className="flex items-center justify-between border-b px-4 py-2.5">
              <span className="flex items-center gap-2 text-[11px] font-medium">
                <Sparkles className="size-3.5" />
                Compose
              </span>
              <KbdGroup size="sm" keys={['⌘', 'J']} />
            </div>
            <div className="space-y-4 p-4">
              <div className="rounded-md border bg-muted/40 px-3 py-2.5">
                <p className="font-mono text-[11px] text-muted-foreground">
                  polite decline — propose next week instead
                  <span className="ml-1 inline-block h-3 w-[5px] translate-y-0.5 animate-pulse bg-foreground/70 motion-reduce:animate-none" />
                </p>
              </div>
              <div className="space-y-2 text-[13px] leading-relaxed">
                <p>Hi Sam,</p>
                <p>
                  Thanks for thinking of us — this week is fully committed on our side, so I’ll
                  have to pass on Thursday. Could we pick it up next week instead? Tuesday or
                  Wednesday afternoon both work.
                </p>
              </div>
              <div className="flex items-center justify-between border-t pt-3">
                <div className="flex items-center gap-2">
                  <Button size="sm" className="pointer-events-none h-7 px-3 text-xs" tabIndex={-1}>
                    Insert draft
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    className="pointer-events-none h-7 px-3 text-xs text-muted-foreground"
                    tabIndex={-1}>
                    Rewrite shorter
                  </Button>
                </div>
                <span className="font-mono text-[10px] text-muted-foreground">
                  via OpenRouter · 1.4 s
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
