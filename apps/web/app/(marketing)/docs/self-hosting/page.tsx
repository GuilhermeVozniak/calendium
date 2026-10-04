import type { Metadata } from 'next';
import Link from 'next/link';
import { ArrowUpRight, Server } from 'lucide-react';

import { GITHUB_URL } from '@/components/marketing/links';
import { SectionHeading } from '@/components/marketing/section-heading';
import { Button } from '@/components/ui/button';

export const metadata: Metadata = {
  title: 'Self-hosting quickstart',
  description:
    'Deploy the whole Calendium stack — Go backend, Postgres, and web app — on your own server in about 15 minutes with Docker Compose. Every feature unlocked, no license keys.',
};

const DOCS_URL = `${GITHUB_URL}/tree/main/docs/self-hosting`;

const requirements = [
  'A Linux host with Docker 29 + Compose v5, make, and openssl',
  'A domain with a DNS A/AAAA record and ports 80 + 443 open (for real HTTPS)',
  'No external auth service — Better Auth is built into the web app',
];

type Step = { title: string; body: React.ReactNode; code?: string };

const steps: Step[] = [
  {
    title: 'Clone and create your .env',
    body: 'Everything the stack needs comes from a single git-ignored .env file — keep your secrets there.',
    code: `git clone ${GITHUB_URL}.git
cd calendium
cp .env.example .env`,
  },
  {
    title: 'Generate the required secrets',
    body: (
      <>
        <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[0.85em]">
          TOKEN_ENCRYPTION_KEY
        </code>{' '}
        encrypts provider refresh tokens at rest (AES-256-GCM) and must be exactly 64 hex
        characters. Back it up and keep it stable — it is never stored in the database.{' '}
        <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[0.85em]">
          INTERNAL_API_SECRET
        </code>{' '}
        (also 64 hex characters, a different value) authenticates the web app&rsquo;s
        server-to-server calls to the API; api, worker and web refuse to start without it.
      </>
    ),
    code: `make gen-secrets       # prints all three secrets as .env lines
# in .env:
POSTGRES_PASSWORD=<a strong password>
TOKEN_ENCRYPTION_KEY=<64 hex chars>       # or: openssl rand -hex 32
INTERNAL_API_SECRET=<another 64 hex chars> # or: openssl rand -hex 32
BETTER_AUTH_SECRET=<from make gen-secrets> # or: openssl rand -base64 32`,
  },
  {
    title: 'Set your instance identity and mode',
    body: 'Self-host mode unlocks every feature and turns Paddle billing off. Point DATABASE_URL at the bundled db service.',
    code: `SELF_HOSTED=true
INSTANCE_NAME=Acme Mail
DOMAIN=mail.example.com
APP_URL=https://mail.example.com
PUBLIC_WEB_URL=https://mail.example.com
DATABASE_URL=postgres://calendium:<password>@db:5432/calendium?sslmode=disable`,
  },
  {
    title: 'Set up authentication (Better Auth)',
    body: 'Email + password works out of the box; you just need a signing secret and your public URL. The Go API verifies Better Auth JWTs via JWKS automatically.',
    code: `BETTER_AUTH_SECRET=<from the previous step>
BETTER_AUTH_URL=https://mail.example.com
NEXT_PUBLIC_API_URL=https://mail.example.com`,
  },
  {
    title: '(Optional) connect Gmail / Outlook and AI',
    body: 'Register your own OAuth apps and add the credentials — these are per-deployment, and unset providers are simply disabled. Set each provider’s redirect URI to your API callback.',
    code: `OAUTH_ALLOWED_REDIRECT_URIS=https://mail.example.com
GOOGLE_CLIENT_ID=...        # redirect: /v1/accounts/callback/google
GOOGLE_CLIENT_SECRET=...
MS_CLIENT_ID=...            # redirect: /v1/accounts/callback/microsoft
MS_CLIENT_SECRET=...
OPENROUTER_API_KEY=...      # optional AI`,
  },
  {
    title: 'Point DNS at your host, then start the stack',
    body: 'Create the A/AAAA record for your domain, open ports 80 + 443, then bring everything up. The api container applies migrations on boot.',
    code: `make self-host-up          # db, api, worker, web + Caddy (auto-HTTPS)
make self-host-logs        # tail all services`,
  },
  {
    title: 'Verify and connect',
    body: (
      <>
        Hit <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[0.85em]">/healthz</code>{' '}
        and <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[0.85em]">/v1/instance</code>{' '}
        to confirm self-host mode. Then open your domain in a browser, or point the desktop/mobile
        apps at your server from their Connect screen.
      </>
    ),
    code: `curl https://mail.example.com/healthz
curl https://mail.example.com/v1/instance
# → { "mode": "self_host", "features": { "billing": false, ... } }`,
  },
];

function CodeBlock({ children }: { children: string }) {
  return (
    <pre className="mt-4 overflow-x-auto rounded-lg border bg-muted/40 p-4 text-xs leading-relaxed">
      <code className="font-mono text-foreground">{children}</code>
    </pre>
  );
}

export default function SelfHostingDocsPage() {
  return (
    <>
      <section className="mx-auto w-full max-w-3xl px-6 pt-20 md:pt-28">
        <div className="inline-flex items-center gap-2 rounded-full border px-3 py-1 font-mono text-xs uppercase tracking-wider text-muted-foreground">
          <Server className="size-3.5" /> Open source · AGPLv3
        </div>
        <SectionHeading
          className="mt-5"
          eyebrow="Self-hosting"
          title="Deploy Calendium in ~15 minutes."
          lede="The blessed path: the repo-root docker-compose.yml brings up the Go backend, Postgres, worker, and web app — with a Caddy reverse proxy and automatic HTTPS. Every feature is unlocked; no license keys."
        />
      </section>

      <section className="mx-auto w-full max-w-3xl px-6 pt-12">
        <div className="rounded-xl border bg-card p-6 shadow-sm">
          <h2 className="text-sm font-semibold">Before you start</h2>
          <ul className="mt-4 space-y-2.5 text-sm text-muted-foreground">
            {requirements.map((req) => (
              <li key={req} className="flex gap-2.5">
                <span aria-hidden className="mt-1.5 size-1.5 shrink-0 rounded-full bg-foreground/40" />
                <span>{req}</span>
              </li>
            ))}
          </ul>
        </div>
      </section>

      <section className="mx-auto w-full max-w-3xl px-6 py-16">
        <ol className="flex flex-col gap-10">
          {steps.map((step, i) => (
            <li key={step.title} className="flex gap-5">
              <span className="flex size-8 shrink-0 items-center justify-center rounded-full border bg-card font-mono text-sm font-medium">
                {i + 1}
              </span>
              <div className="min-w-0 flex-1 pt-0.5">
                <h3 className="text-base font-semibold tracking-tight">{step.title}</h3>
                <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{step.body}</p>
                {step.code && <CodeBlock>{step.code}</CodeBlock>}
              </div>
            </li>
          ))}
        </ol>
      </section>

      <section className="border-t">
        <div className="mx-auto w-full max-w-3xl px-6 py-16">
          <div className="rounded-2xl border bg-card p-8 shadow-sm">
            <h2 className="text-xl font-semibold tracking-tight">Read the full guide</h2>
            <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
              This is the short version. The repository has the complete self-hosting
              documentation — configuration reference, provider setup, pointing the apps at your
              server, upgrades, security hardening, and troubleshooting.
            </p>
            <div className="mt-6 flex flex-wrap gap-3">
              <Button asChild>
                <a href={DOCS_URL} target="_blank" rel="noreferrer noopener">
                  Full self-hosting docs
                  <ArrowUpRight />
                </a>
              </Button>
              <Button asChild variant="outline">
                <a href={GITHUB_URL} target="_blank" rel="noreferrer noopener">
                  View on GitHub
                  <ArrowUpRight />
                </a>
              </Button>
              <Button asChild variant="ghost" className="text-muted-foreground">
                <Link href="/pricing">Prefer Cloud? $50/year</Link>
              </Button>
            </div>
          </div>
        </div>
      </section>
    </>
  );
}
