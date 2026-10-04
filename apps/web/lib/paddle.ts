/**
 * Paddle.js v2 loader (overlay checkout, docs/payments.md). Loaded only on
 * /checkout; the script tag is injected once and resolved from window.Paddle.
 */

export const PADDLE_JS_SRC = 'https://cdn.paddle.com/paddle/v2/paddle.js';

export interface PaddleCheckoutSettings {
  successUrl?: string;
  displayMode?: 'overlay' | 'inline';
  /** false hides the overlay's "change email" control, pinning the transaction's customer. */
  allowLogout?: boolean;
}

export interface PaddleInitializeOptions {
  token: string;
  eventCallback?: (event: { name: string }) => void;
  checkout?: { settings: PaddleCheckoutSettings };
}

export interface PaddleJs {
  Environment: { set(env: 'sandbox' | 'production'): void };
  Initialize(options: PaddleInitializeOptions): void;
  /** Reopens the overlay; settings default to those passed to Initialize. */
  Checkout: { open(options: { transactionId: string }): void };
}

declare global {
  interface Window {
    Paddle?: PaddleJs;
  }
}

const SCRIPT_ID = 'paddle-js';

/** Resolves window.Paddle, injecting the CDN script once if needed. */
export function loadPaddleJs(doc: Document = document): Promise<PaddleJs> {
  if (window.Paddle) return Promise.resolve(window.Paddle);
  return new Promise((resolve, reject) => {
    const existing = doc.getElementById(SCRIPT_ID) as HTMLScriptElement | null;
    const script = existing ?? doc.createElement('script');
    const done = () =>
      window.Paddle ? resolve(window.Paddle) : reject(new Error('Paddle.js loaded without window.Paddle'));
    script.addEventListener('load', done, { once: true });
    script.addEventListener('error', () => reject(new Error('Could not load Paddle.js')), { once: true });
    if (!existing) {
      script.id = SCRIPT_ID;
      script.src = PADDLE_JS_SRC;
      script.async = true;
      doc.head.appendChild(script);
    }
  });
}

/** Environment.set('sandbox') when NEXT_PUBLIC_PADDLE_ENV=sandbox, then Initialize with the fixed success URL. */
export function initPaddle(
  paddle: PaddleJs,
  opts: { token: string; env: string | undefined; successUrl: string; eventCallback?: (event: { name: string }) => void }
): void {
  if (opts.env === 'sandbox') paddle.Environment.set('sandbox');
  paddle.Initialize({
    token: opts.token,
    eventCallback: opts.eventCallback,
    // allowLogout:false locks the overlay to the customer our backend attached to
    // the transaction; a buyer switching email there would be billed as a
    // customer no Calendium account owns, and the webhook would drop it.
    checkout: { settings: { successUrl: opts.successUrl, displayMode: 'overlay', allowLogout: false } },
  });
}
