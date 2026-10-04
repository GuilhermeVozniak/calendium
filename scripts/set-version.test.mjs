import { cp, mkdir, mkdtemp, readFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

import { checkVersion, main, setVersion, VERSION_FILES } from './set-version.mjs';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

/** Temp copy of the real app.json / wails.json so tests never touch the tree. */
async function fixture() {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'set-version-'));
  for (const { file } of VERSION_FILES) {
    await mkdir(path.dirname(path.join(dir, file)), { recursive: true });
    await cp(path.join(ROOT, file), path.join(dir, file));
  }
  return dir;
}

function changedLines(before, after) {
  const a = before.split('\n');
  const b = after.split('\n');
  expect(b.length).toBe(a.length);
  return a.map((line, i) => [line, b[i]]).filter(([x, y]) => x !== y);
}

describe('set-version', () => {
  it('covers app.json expo.version and wails.json info.productVersion', () => {
    expect(VERSION_FILES.map((f) => f.file)).toEqual(['apps/mobile/app.json', 'apps/desktop/wails.json']);
  });

  it('stamps both files touching only the version line (key order, 2-space indent, trailing newline kept)', async () => {
    const dir = await fixture();
    const before = Object.fromEntries(
      await Promise.all(VERSION_FILES.map(async ({ file }) => [file, await readFile(path.join(dir, file), 'utf8')]))
    );
    const changed = await setVersion(dir, '7.8.9');
    expect(changed).toEqual(['apps/mobile/app.json', 'apps/desktop/wails.json']);
    for (const { file, path: keys } of VERSION_FILES) {
      const after = await readFile(path.join(dir, file), 'utf8');
      const diff = changedLines(before[file], after);
      expect(diff).toHaveLength(1);
      expect(diff[0][1]).toContain('"7.8.9"');
      expect(after.endsWith('\n')).toBe(true);
      expect(Object.keys(JSON.parse(after))).toEqual(Object.keys(JSON.parse(before[file])));
      expect(keys.reduce((o, k) => o[k], JSON.parse(after))).toBe('7.8.9');
    }
  });

  it('is idempotent', async () => {
    const dir = await fixture();
    await setVersion(dir, '7.8.9');
    expect(await setVersion(dir, '7.8.9')).toEqual([]);
    expect(await checkVersion(dir, '7.8.9')).toEqual([]);
  });

  // Review Focus 1: humans copy tag names; "v1.2.3" must be refused, not stamped.
  it('rejects anything that is not X.Y.Z, including a v prefix and prereleases', async () => {
    const dir = await fixture();
    for (const bad of ['v1.2.3', '1.2', '1.2.3-rc.1', '1.2.3.4', '', 'latest']) {
      await expect(setVersion(dir, bad)).rejects.toThrow(/is not X\.Y\.Z/);
      await expect(checkVersion(dir, bad)).rejects.toThrow(/is not X\.Y\.Z/);
    }
    expect(await main(['v1.2.3'], dir)).toBe(1);
    const untouched = await readFile(path.join(dir, 'apps/mobile/app.json'), 'utf8');
    expect(untouched).not.toContain('v1.2.3');
  });

  it('--check exits 1 on drift and 0 once stamped; no args is a usage error', async () => {
    const dir = await fixture();
    expect(await main(['--check', '7.8.9'], dir)).toBe(1);
    expect(await main(['7.8.9'], dir)).toBe(0);
    expect(await main(['7.8.9', '--check'], dir)).toBe(0);
    expect(await main([], dir)).toBe(2);
  });

  it('--check never writes', async () => {
    const dir = await fixture();
    const before = await readFile(path.join(dir, 'apps/desktop/wails.json'), 'utf8');
    await main(['--check', '7.8.9'], dir);
    expect(await readFile(path.join(dir, 'apps/desktop/wails.json'), 'utf8')).toBe(before);
  });
});
