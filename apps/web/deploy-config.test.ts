// @vitest-environment node
import { readFileSync } from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

import { proxyTrustFromEnv } from '@/lib/client-ip';

/**
 * The shipped deployment shape (root docker-compose.yml + .env.example) must
 * trust the bundled Caddy, or every client shares one auth rate-limit bucket
 * (whole-branch review I1).
 */
const ROOT = path.resolve(__dirname, '../..');
const read = (file: string) => readFileSync(path.join(ROOT, file), 'utf8');

/** The lines of one top-level Compose service, up to the next service or top-level key. */
function composeService(yml: string, name: string): string {
  const lines = yml.split('\n');
  const start = lines.findIndex((l) => l === `  ${name}:`);
  if (start === -1) throw new Error(`docker-compose.yml has no ${name} service`);
  const end = lines.findIndex((l, i) => i > start && /^ {0,2}\S/.test(l));
  return lines.slice(start, end === -1 ? undefined : end).join('\n');
}

describe('deployment defaults for TRUST_PROXY', () => {
  it('docker-compose.yml defaults TRUST_PROXY to true on the web service', () => {
    const web = composeService(read('docker-compose.yml'), 'web');
    expect(web).toMatch(/^ {6}TRUST_PROXY: \$\{TRUST_PROXY:-true\}$/m);
  });

  it('the root .env.example ships TRUST_PROXY=true', () => {
    const lines = read('.env.example').split('\n');
    expect(lines.filter((l) => l.startsWith('TRUST_PROXY='))).toEqual(['TRUST_PROXY=true']);
  });

  it('the shipped default trusts the bundled Caddy on the Compose network', () => {
    expect(proxyTrustFromEnv({ TRUST_PROXY: 'true' }).proxies.check('172.18.0.5', 'ipv4')).toBe(true);
  });
});
