import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import PrivacyPage from './privacy/page';
import TermsPage from './terms/page';

describe('privacy page', () => {
  it('names the background-AI toggle, every third party and the account controls', () => {
    render(<PrivacyPage />);
    expect(screen.getByText(/Last updated October 2026/)).toBeInTheDocument();
    expect(screen.getByText(/Background AI processing/)).toBeInTheDocument();
    for (const vendor of ['OpenRouter', 'Paddle', 'Todoist', 'HubSpot', 'Open-Meteo', 'Nominatim', 'OSRM', 'APNs', 'FCM']) {
      expect(screen.getAllByText(new RegExp(vendor)).length).toBeGreaterThan(0);
    }
    expect(screen.getByText(/Delete account/)).toBeInTheDocument();
    expect(screen.getByText(/Download my data/)).toBeInTheDocument();
    expect(screen.queryByText(/Stripe/)).toBeNull();
    expect(screen.queryByText(/nothing runs in the background/)).toBeNull();
  });

  // Track C ⚠️: the export is the data you created and stored, not "everything".
  it('describes the export as the data you created and stored and lists what it contains', () => {
    render(<PrivacyPage />);
    expect(screen.getByText(/download the data\s+you created and stored in Calendium/)).toBeInTheDocument();
    const exportCopy = screen.getByText(/The export contains/);
    for (const category of [
      'mail mirror metadata and messages',
      'calendar events',
      'notes',
      'tasks',
      'snippets',
      'settings',
      'booking pages and bookings',
      'thread comments',
      'calendar subscriptions',
      'classifier prompts',
      'voice profile',
    ]) {
      expect(exportCopy.textContent).toContain(category);
    }
    expect(screen.queryByText(/copy of everything/)).toBeNull();
  });
});

describe('terms page', () => {
  it('names Paddle as merchant of record with its refund policy and the export-before-delete sentence', () => {
    render(<TermsPage />);
    expect(screen.getByText(/Last updated October 2026/)).toBeInTheDocument();
    expect(screen.getAllByText(/Paddle/).length).toBeGreaterThan(0);
    expect(screen.getByText(/Refund requests are handled by Paddle under its refund policy/)).toBeInTheDocument();
    expect(screen.getByText(/download a copy of your data from Settings/)).toBeInTheDocument();
    expect(screen.queryByText(/Stripe/)).toBeNull();
    expect(screen.queryByText(/non-refundable/)).toBeNull();
  });
});
