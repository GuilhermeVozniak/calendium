import type { NextConfig } from 'next';
import path from 'node:path';

import { securityHeaders } from './lib/security-headers';

const nextConfig: NextConfig = {
  // Emit a self-contained server bundle (.next/standalone) for the Docker image.
  output: 'standalone',
  // Trace from the monorepo root so the workspace dep @calendium/shared and the
  // hoisted node_modules are included in the standalone output.
  outputFileTracingRoot: path.join(__dirname, '../../'),
  transpilePackages: ['@calendium/shared'],
  // Static security headers on every route; the per-request nonce CSP is
  // added by middleware.ts, /offline gets its fixed CSP here.
  async headers() {
    return securityHeaders();
  },
};

export default nextConfig;
