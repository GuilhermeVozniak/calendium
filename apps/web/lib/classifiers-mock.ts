import type { AiClassifier, ClassifierInput } from '@calendium/shared';

/**
 * In-memory mock for AI classifiers, used as a transparent fallback while the
 * Go backend is unreachable (explicit demo mode only — see lib/demo.ts).
 */

let store: AiClassifier[] | null = null;
let nextId = 1;

function seed(): AiClassifier[] {
  return [
    {
      id: 'clf-recruiter',
      name: 'Recruiter outreach',
      prompt: 'Cold outreach from a recruiter or staffing agency about a job opportunity.',
      targetSplit: 'other',
      labelName: 'Recruiting',
      enabled: true,
    },
    {
      id: 'clf-invoices',
      name: 'Invoices & receipts',
      prompt: 'Automated invoice, receipt, or billing confirmation from a vendor.',
      targetSplit: undefined,
      labelName: 'Finance',
      enabled: true,
    },
  ];
}

function getStore(): AiClassifier[] {
  store ??= seed();
  return store;
}

export const classifiersMock = {
  list(): AiClassifier[] {
    return getStore().map((c) => ({ ...c }));
  },

  create(input: ClassifierInput): AiClassifier {
    const classifier: AiClassifier = { id: `clf-local-${nextId++}`, ...input };
    getStore().unshift(classifier);
    return { ...classifier };
  },

  update(id: string, input: ClassifierInput): AiClassifier {
    const s = getStore();
    const idx = s.findIndex((c) => c.id === id);
    if (idx === -1) throw new Error('Classifier not found');
    s[idx] = { id, ...input };
    return { ...s[idx]! };
  },

  remove(id: string): void {
    store = getStore().filter((c) => c.id !== id);
  },
};
