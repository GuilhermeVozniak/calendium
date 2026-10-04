import type { InstanceInfo } from '@calendium/shared';
import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const state: { data: InstanceInfo | undefined; isPending: boolean } = { data: undefined, isPending: false };
vi.mock('@/lib/use-instance', () => ({ useInstance: () => state }));

import { SELF_HOSTING_DOCS_HREF, START_TRIAL_HREF } from './links';
import { StartTrialCta } from './start-trial-cta';

function instance(billing: boolean): InstanceInfo {
  return {
    name: 'C', mode: billing ? 'cloud' : 'self_host', version: 't', authBaseUrl: 'x', authProviders: ['email'], undoSendSeconds: 15, webUrl: 'http://localhost:3000',
    features: { billing, google: false, microsoft: false, ai: false, push: false },
  };
}

beforeEach(() => {
  state.data = undefined;
  state.isPending = false;
});

describe('StartTrialCta', () => {
  it('links to the trial funnel when the server bills', () => {
    state.data = instance(true);
    render(<StartTrialCta />);
    expect(screen.getByRole('link', { name: /Start free trial/ })).toHaveAttribute('href', START_TRIAL_HREF);
  });
  it('offers self-hosting when billing is off', () => {
    state.data = instance(false);
    render(<StartTrialCta />);
    expect(screen.queryByRole('link', { name: /Start free trial/ })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Self-host for free/ })).toHaveAttribute('href', SELF_HOSTING_DOCS_HREF);
  });
  it('is disabled while discovery is pending', () => {
    state.isPending = true;
    render(<StartTrialCta />);
    expect(screen.getByRole('button', { name: /Start free trial/ })).toBeDisabled();
  });
});
