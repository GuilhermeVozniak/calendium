import type { Metadata } from 'next';

import { CheckoutSuccessClient } from './success-client';

export const metadata: Metadata = { title: 'Payment received', robots: { index: false } };

/** Public: Paddle's success URL. */
export default function CheckoutSuccessPage() {
  return <CheckoutSuccessClient />;
}
