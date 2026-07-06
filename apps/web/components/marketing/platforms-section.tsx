import { AppWindow, Globe, Smartphone } from 'lucide-react';

import { SectionHeading } from './section-heading';

const platforms = [
  {
    icon: Globe,
    name: 'Web',
    copy: 'The full client in any browser. Nothing to install, nothing to update.',
  },
  {
    icon: AppWindow,
    name: 'Desktop',
    copy: 'A native shell with global shortcuts, a menu-bar peek, and dock badges.',
  },
  {
    icon: Smartphone,
    name: 'iOS & Android',
    copy: 'Triage anywhere, with push notifications only for mail that matters.',
  },
];

export function PlatformsSection() {
  return (
    <section className="border-t">
      <div className="mx-auto w-full max-w-6xl px-6 py-24 md:py-32">
        <SectionHeading
          eyebrow="Everywhere"
          title="One subscription. Every screen."
          lede="Pay once a year, use it on everything you own. Desktop opens checkout in your browser and mobile never asks for money — no app-store toll, anywhere."
        />
        <div className="mt-12 grid divide-y overflow-hidden rounded-xl border sm:grid-cols-3 sm:divide-x sm:divide-y-0">
          {platforms.map((platform) => (
            <div key={platform.name} className="bg-card p-7">
              <platform.icon className="size-5 text-muted-foreground" strokeWidth={1.75} />
              <p className="mt-4 text-sm font-semibold">{platform.name}</p>
              <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{platform.copy}</p>
            </div>
          ))}
        </div>
        <p className="mt-5 font-mono text-xs text-muted-foreground">
          one account · one $50/year plan · signed in everywhere
        </p>
      </div>
    </section>
  );
}
