import type { NextConfig } from 'next';
import path from 'node:path';

const nextConfig: NextConfig = {
  // Emit a self-contained server bundle (.next/standalone) for the Docker image.
  output: 'standalone',
  // Trace from the monorepo root so the workspace dep @calendium/shared and the
  // hoisted node_modules are included in the standalone output.
  outputFileTracingRoot: path.join(__dirname, '../../'),
  transpilePackages: ['@calendium/shared'],
};

export default nextConfig;
