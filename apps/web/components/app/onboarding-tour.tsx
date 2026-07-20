'use client';

import * as React from 'react';
import { usePathname, useRouter } from 'next/navigation';

import { Button } from '@/components/ui/button';
import { Kbd } from '@/components/ui/kbd';
import { maybeAutoStartTour, nextTourStep, setTourUser, skipTour, useTourState } from '@/lib/tour-state';
import { tourSteps, type TourStep } from '@/lib/tour-steps';

const POPOVER_WIDTH = 320;
const POPOVER_EST_HEIGHT = 180;
const MARGIN = 12;

/**
 * Concierge tour engine: anchored popovers over `data-tour="..."` targets.
 *
 * Mounted once in the authenticated app shell; auto-starts on a user's first
 * visit (state in lib/tour-state.ts) and can be re-run from the command
 * palette. Each step resolves its anchor with a short retry window (covers
 * navigation + mount), and a step whose anchor never appears — wrong page
 * that refused to navigate, responsive-hidden element, feature not rendered —
 * is skipped instead of blocking the tour.
 */
export function OnboardingTour({
  userId,
  steps = tourSteps,
  anchorRetryMs = 250,
  anchorRetryLimit = 12,
}: {
  /** The signed-in user's id — scopes the persisted tour state per user. */
  userId: string;
  steps?: TourStep[];
  anchorRetryMs?: number;
  anchorRetryLimit?: number;
}) {
  const { active, stepIndex } = useTourState();
  const pathname = usePathname();
  const router = useRouter();
  const step = active ? steps[stepIndex] : undefined;
  const [rect, setRect] = React.useState<DOMRect | null>(null);

  // First-run entry point: the shell only mounts this for signed-in users.
  // Attaching the user scope first makes the auto-start read (and any later
  // completion write) hit that user's own key.
  React.useEffect(() => {
    setTourUser(userId);
    maybeAutoStartTour();
  }, [userId]);

  // Walk the user to the step's page — concierge style, no dead popovers.
  React.useEffect(() => {
    if (!step) return;
    if (step.page !== 'any' && !pathname.startsWith(`/${step.page}`)) {
      router.push(`/${step.page}`);
    }
  }, [step, pathname, router]);

  // Resolve the anchor; retry briefly (page mount, navigation), then skip.
  React.useEffect(() => {
    if (!step) {
      setRect(null);
      return;
    }
    let cancelled = false;
    let attempts = 0;
    let timer: number | undefined;
    setRect(null);

    function locate() {
      if (cancelled) return;
      const el = document.querySelector(`[data-tour="${step!.target}"]`);
      const r = el?.getBoundingClientRect();
      if (el && r && (r.width > 0 || r.height > 0)) {
        setRect(r);
        return;
      }
      attempts += 1;
      if (attempts > anchorRetryLimit) {
        // Missing/hidden anchor: never block the tour — move on.
        nextTourStep(steps.length);
        return;
      }
      timer = window.setTimeout(locate, anchorRetryMs);
    }
    locate();

    // Keep the popover glued to its anchor across resizes/scrolls.
    function remeasure() {
      const el = document.querySelector(`[data-tour="${step!.target}"]`);
      if (el) setRect(el.getBoundingClientRect());
    }
    window.addEventListener('resize', remeasure);
    window.addEventListener('scroll', remeasure, true);
    return () => {
      cancelled = true;
      if (timer) window.clearTimeout(timer);
      window.removeEventListener('resize', remeasure);
      window.removeEventListener('scroll', remeasure, true);
    };
    // No pathname dep: the retry window above already covers anchors that
    // appear after the step's router.push settles.
  }, [step, steps.length, anchorRetryMs, anchorRetryLimit]);

  if (!step || !rect) return null;

  const last = stepIndex + 1 === steps.length;
  const viewportW = typeof window === 'undefined' ? 1024 : window.innerWidth;
  const viewportH = typeof window === 'undefined' ? 768 : window.innerHeight;
  const left = Math.min(Math.max(MARGIN, rect.left), Math.max(MARGIN, viewportW - POPOVER_WIDTH - MARGIN));
  const below = rect.bottom + MARGIN;
  const top =
    below + POPOVER_EST_HEIGHT <= viewportH ? below : Math.max(MARGIN, rect.top - POPOVER_EST_HEIGHT - MARGIN);

  return (
    <>
      <div
        aria-hidden
        className="ring-primary pointer-events-none fixed z-[70] rounded-md ring-2"
        style={{ top: rect.top - 4, left: rect.left - 4, width: rect.width + 8, height: rect.height + 8 }}
      />
      {/* Plain role="dialog" (no data-state) so single-key shortcuts keep
          working while the tour points at them — lib/shortcuts.ts only
          suppresses bindings behind [role="dialog"][data-state="open"]. */}
      <div
        role="dialog"
        aria-label={step.title}
        data-testid="tour-popover"
        className="bg-popover text-popover-foreground fixed z-[71] w-80 rounded-lg border p-4 shadow-lg"
        style={{ top, left }}
      >
        <p className="text-sm font-semibold">{step.title}</p>
        <p className="text-muted-foreground mt-1 text-sm">{step.body}</p>
        {step.shortcut && (
          <p className="text-muted-foreground mt-2 flex items-center gap-1.5 text-xs">
            Try it:
            <Kbd size="sm">{step.shortcut}</Kbd>
          </p>
        )}
        <div className="mt-3 flex items-center gap-2">
          <span className="text-muted-foreground text-xs">{`${stepIndex + 1} of ${steps.length}`}</span>
          <Button variant="ghost" size="sm" className="ml-auto" onClick={() => skipTour()}>
            Skip tour
          </Button>
          <Button size="sm" onClick={() => nextTourStep(steps.length)}>
            {last ? 'Done' : 'Next'}
          </Button>
        </div>
      </div>
    </>
  );
}
