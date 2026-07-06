/**
 * Marketing CTA destinations. The (app) route group owns these routes;
 * signed-out visitors go through sign-in and land on billing to start the trial.
 */
export const APP_HREF = '/mail';

/**
 * Start-trial funnel: sign in, then land on the billing tab of settings. The
 * `next` value carries its own query string, so it is URL-encoded to survive as
 * a single param (signin honors relative `next` paths for email + social).
 */
export const START_TRIAL_HREF = `/signin?next=${encodeURIComponent('/settings?tab=billing')}`;
export const SUPPORT_EMAIL = 'support@calendium.app';

/** Open-core: the self-hosting guide and the public source repository. */
export const SELF_HOSTING_DOCS_HREF = '/docs/self-hosting';
export const GITHUB_URL = 'https://github.com/calendium/calendium';
