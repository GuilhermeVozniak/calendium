import type { Metadata } from 'next';

import { CheckoutClient } from './checkout-client';

export const metadata: Metadata = { title: 'Checkout', robots: { index: false } };

/** Public (no auth gate): Paddle's default payment link lands here. */
export default function CheckoutPage() {
  return <CheckoutClient />;
}
