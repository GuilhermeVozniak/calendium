/** Container HEALTHCHECK target (apps/web/Dockerfile); never prerendered. */
export const dynamic = 'force-dynamic';

export function GET(): Response {
  return Response.json({ status: 'ok' });
}
