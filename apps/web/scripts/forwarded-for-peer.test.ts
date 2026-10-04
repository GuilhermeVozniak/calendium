// @vitest-environment node
import http from 'node:http';
import type { AddressInfo } from 'node:net';

import { afterAll, beforeAll, describe, expect, it } from 'vitest';

import peer from './forwarded-for-peer.cjs';

/**
 * Next fills X-Forwarded-For from the socket only when it is ABSENT, so a
 * client talking to the web port directly would choose its own rate-limit
 * key. The preload makes the server append its socket peer instead, the way
 * a proxy does, so the right-most entry is always the real immediate peer.
 */
describe('forwarded-for-peer preload', () => {
  it('appends the socket peer to an existing header and sets it when absent', () => {
    const withHeader = { socket: { remoteAddress: '203.0.113.9' }, headers: { 'x-forwarded-for': '6.6.6.6' } };
    peer.appendPeer(withHeader);
    expect(withHeader.headers['x-forwarded-for']).toBe('6.6.6.6, 203.0.113.9');

    const without: { socket: { remoteAddress: string }; headers: Record<string, string> } = { socket: { remoteAddress: '203.0.113.9' }, headers: {} };
    peer.appendPeer(without);
    expect(without.headers['x-forwarded-for']).toBe('203.0.113.9');
  });

  it('appends once per request even if the hook sees it twice', () => {
    const req = { socket: { remoteAddress: '203.0.113.9' }, headers: { 'x-forwarded-for': '6.6.6.6' } };
    peer.appendPeer(req);
    peer.appendPeer(req);
    expect(req.headers['x-forwarded-for']).toBe('6.6.6.6, 203.0.113.9');
  });

  describe('installed on a real http.Server', () => {
    let server: http.Server;
    let seen: string | undefined;
    let port: number;

    beforeAll(async () => {
      peer.install();
      peer.install(); // idempotent
      server = http.createServer((req, res) => {
        seen = req.headers['x-forwarded-for'] as string | undefined;
        res.end('ok');
      });
      await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
      port = (server.address() as AddressInfo).port;
    });

    afterAll(async () => {
      await new Promise((resolve) => server.close(resolve));
    });

    it('a client-supplied X-Forwarded-For can no longer be the right-most entry', async () => {
      await fetch(`http://127.0.0.1:${port}/`, { headers: { 'x-forwarded-for': '6.6.6.6' } });
      expect(seen).toBe('6.6.6.6, 127.0.0.1');
      await fetch(`http://127.0.0.1:${port}/`);
      expect(seen).toBe('127.0.0.1');
      expect(peer.isInstalled()).toBe(true);
    });
  });
});
