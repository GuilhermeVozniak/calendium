const logos = ['Meridian Labs', 'Harbor & Co', 'Alpine Systems', 'Fieldwork', 'Mono Studio', 'Northbeam'];

const quotes = [
  {
    quote:
      'I emptied three days of backlog in forty minutes. Now email takes eleven minutes, twice a day.',
    name: 'Priya N.',
    role: 'Founder, Meridian Labs',
  },
  {
    quote: 'The calendar peek replaced two apps. Sharing availability went from a chore to a keystroke.',
    name: 'Daniel R.',
    role: 'Staff engineer, Harbor & Co',
  },
  {
    quote: 'The first software in years that feels faster than I am.',
    name: 'June K.',
    role: 'Chief of staff, Alpine',
  },
];

export function SocialProof() {
  return (
    <section className="border-t">
      <div className="mx-auto w-full max-w-6xl px-6 py-20 md:py-24">
        <p className="text-center font-mono text-xs uppercase tracking-[0.2em] text-muted-foreground">
          Kept close by people who send a lot of email
        </p>
        <div className="mt-8 flex flex-wrap items-center justify-center gap-x-10 gap-y-4">
          {logos.map((logo) => (
            <span
              key={logo}
              className="text-sm font-semibold tracking-tight text-muted-foreground/60">
              {logo}
            </span>
          ))}
        </div>
        <div className="mt-16 grid gap-10 md:grid-cols-3">
          {quotes.map((item) => (
            <figure key={item.name} className="border-l-2 pl-6">
              <blockquote className="text-pretty text-sm leading-relaxed">
                “{item.quote}”
              </blockquote>
              <figcaption className="mt-4 text-xs text-muted-foreground">
                <span className="font-medium text-foreground">{item.name}</span> · {item.role}
              </figcaption>
            </figure>
          ))}
        </div>
      </div>
    </section>
  );
}
