/**
 * Preloaded into the production web server (`node --require` in the
 * Dockerfile CMD) before Next.js creates its HTTP server.
 *
 * Next.js sets X-Forwarded-For from the socket only when the header is
 * ABSENT, so a client talking to the web port directly could choose its own
 * rate-limit key by sending one. This appends the socket peer to the header
 * — exactly what an appending reverse proxy does — so the right-most entry
 * is always the real immediate peer. lib/client-ip.ts keys on it.
 */
'use strict';

const http = require('node:http');

const INSTALLED = Symbol.for('calendium.forwardedForPeer');
const seen = new WeakSet();

/** @param {{ socket?: { remoteAddress?: string }, headers: Record<string, string | string[] | undefined> }} req */
function appendPeer(req) {
  if (seen.has(req)) return;
  seen.add(req);
  const peer = req.socket?.remoteAddress;
  if (!peer) return;
  const prior = req.headers['x-forwarded-for'];
  const value = Array.isArray(prior) ? prior.join(', ') : prior;
  req.headers['x-forwarded-for'] = value?.trim() ? `${value}, ${peer}` : peer;
}

/** Patches http.Server once per process (also covers servers created earlier). */
function install() {
  if (globalThis[INSTALLED]) return;
  globalThis[INSTALLED] = true;
  const emit = http.Server.prototype.emit;
  http.Server.prototype.emit = function emitWithPeer(event, ...args) {
    if (event === 'request' && args[0]) appendPeer(args[0]);
    return emit.call(this, event, ...args);
  };
}

/** Whether this process appends the peer (instrumentation.ts warns in production when it does not). */
function isInstalled() {
  return globalThis[INSTALLED] === true;
}

module.exports = { appendPeer, install, isInstalled };

install();
