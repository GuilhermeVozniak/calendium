import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { InboxZero, ZERO_SCENES, sceneForDate } from './inbox-zero';

describe('inbox zero celebration', () => {
  it('has at least five scenes with copy and a gradient', () => {
    expect(ZERO_SCENES.length).toBeGreaterThanOrEqual(5);
    for (const scene of ZERO_SCENES) {
      expect(scene.headline).toBeTruthy();
      expect(scene.gradient).toMatch(/from-/);
    }
  });

  it('rotates deterministically by day of year', () => {
    const a = sceneForDate(new Date('2026-07-17T10:00:00Z'));
    const b = sceneForDate(new Date('2026-07-17T23:00:00Z'));
    const c = sceneForDate(new Date('2026-07-18T10:00:00Z'));
    expect(a.id).toBe(b.id); // same day, same scene
    expect(c.id).not.toBe(a.id); // consecutive days differ (scenes >= 2)
  });

  it('renders the scene headline and the inbox-zero badge', () => {
    const date = new Date('2026-07-17T10:00:00Z');
    render(<InboxZero date={date} />);
    expect(screen.getByText(sceneForDate(date).headline)).toBeInTheDocument();
    expect(screen.getByText("You're at Inbox Zero")).toBeInTheDocument();
  });
});
