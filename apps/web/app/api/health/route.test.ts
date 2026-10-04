// @vitest-environment node
import { describe, expect, it } from 'vitest';

import { dynamic, GET } from '@/app/api/health/route';

describe('GET /api/health', () => {
  it('answers 200 {status:ok} and is never statically cached', async () => {
    const res = GET();
    expect(res.status).toBe(200);
    await expect(res.json()).resolves.toEqual({ status: 'ok' });
    expect(dynamic).toBe('force-dynamic');
  });
});
