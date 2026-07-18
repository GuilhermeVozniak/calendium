import type { AiClassifier, ClassifierInput } from '@calendium/shared';

import { getApiClient } from '@/lib/api';
import { classifiersMock } from '@/lib/classifiers-mock';
import { DEMO_MODE } from '@/lib/demo';

/**
 * Data-access wrappers for AI classifiers (Settings > AI classifiers). They
 * hit the real API and, in explicit demo mode only (lib/demo.ts), fall back to
 * the in-memory mock (lib/classifiers-mock.ts). Outside demo mode failures
 * propagate so the UI shows real loading / empty / error states.
 */

export async function fetchClassifiers(): Promise<AiClassifier[]> {
  try {
    return await getApiClient().listClassifiers();
  } catch (err) {
    if (DEMO_MODE) return classifiersMock.list();
    throw err;
  }
}

export async function createClassifierApi(input: ClassifierInput): Promise<AiClassifier> {
  try {
    return await getApiClient().createClassifier(input);
  } catch (err) {
    if (DEMO_MODE) return classifiersMock.create(input);
    throw err;
  }
}

export async function updateClassifierApi(id: string, input: ClassifierInput): Promise<AiClassifier> {
  try {
    return await getApiClient().updateClassifier(id, input);
  } catch (err) {
    if (DEMO_MODE) return classifiersMock.update(id, input);
    throw err;
  }
}

export async function deleteClassifierApi(id: string): Promise<void> {
  try {
    await getApiClient().deleteClassifier(id);
  } catch (err) {
    if (DEMO_MODE) {
      classifiersMock.remove(id);
      return;
    }
    throw err;
  }
}
