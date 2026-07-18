import { describe, expect, it } from 'vitest';

import { companyFromDomain, faviconUrl, gravatarUrl } from '@/lib/contact-utils';

// Known vector: sha256("gui336699@gmail.com") — verified independently via
// `python3 -c "import hashlib; print(hashlib.sha256(b'gui336699@gmail.com').hexdigest())"`.
const KNOWN_HASH = 'c5776afca0e64879ed4f611ad3fcaf6f8be69f34e098ae267fb7a579671b1dd6';

describe('gravatarUrl', () => {
  it('hashes the lowercased, trimmed email with SHA-256 and appends ?d=404', async () => {
    const url = await gravatarUrl('gui336699@gmail.com');
    expect(url).toBe(`https://www.gravatar.com/avatar/${KNOWN_HASH}?d=404`);
  });

  it('lowercases and trims the email before hashing (matches the known vector)', async () => {
    const url = await gravatarUrl('  GUI336699@GMAIL.COM  ');
    expect(url).toBe(`https://www.gravatar.com/avatar/${KNOWN_HASH}?d=404`);
  });
});

describe('faviconUrl', () => {
  it("builds the DuckDuckGo icon URL for a domain", () => {
    expect(faviconUrl('acme.co.uk')).toBe('https://icons.duckduckgo.com/ip3/acme.co.uk.ico');
  });
});

describe('companyFromDomain', () => {
  it('capitalizes the second-level label for a plain .com domain', () => {
    expect(companyFromDomain('northwind.com')).toBe('Northwind');
  });

  it('handles a two-label public suffix (acme.co.uk -> Acme)', () => {
    expect(companyFromDomain('acme.co.uk')).toBe('Acme');
  });

  it('returns an empty string for freemail domains', () => {
    expect(companyFromDomain('gmail.com')).toBe('');
    expect(companyFromDomain('outlook.com')).toBe('');
    expect(companyFromDomain('yahoo.com')).toBe('');
    expect(companyFromDomain('icloud.com')).toBe('');
    expect(companyFromDomain('hotmail.com')).toBe('');
    expect(companyFromDomain('proton.me')).toBe('');
  });

  it('is case-insensitive on the domain', () => {
    expect(companyFromDomain('ACME.CO.UK')).toBe('Acme');
    expect(companyFromDomain('GMAIL.com')).toBe('');
  });
});
