#!/usr/bin/env node
// scripts/set-version.mjs — the vX.Y.Z git tag is the single source of truth
// for every shipped version. This stamps X.Y.Z into the files that carry one:
//
//   apps/mobile/app.json      expo.version         (commit before tagging;
//                                                   release.yml --check refuses drift)
//   apps/desktop/wails.json   info.productVersion  (Wails templates it into
//                                                   Info.plist + the NSIS installer)
//
// Usage:  node scripts/set-version.mjs X.Y.Z [--check]
//   --check  write nothing; exit 1 when any file disagrees with X.Y.Z.
// Exit codes: 0 ok, 1 invalid version / drift, 2 usage.
//
// Only the exact `"<key>": "<current>"` text is replaced, so formatting and
// key order of both JSON files survive byte-for-byte.
import { readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const SEMVER = /^\d+\.\d+\.\d+$/;

export const VERSION_FILES = [
  { file: 'apps/mobile/app.json', path: ['expo', 'version'], key: 'version' },
  { file: 'apps/desktop/wails.json', path: ['info', 'productVersion'], key: 'productVersion' },
];

function assertSemver(version) {
  if (typeof version !== 'string' || !SEMVER.test(version)) {
    throw new Error(`set-version: "${version}" is not X.Y.Z (no "v" prefix, no prerelease, no build metadata)`);
  }
}

function getPath(obj, keys) {
  return keys.reduce((o, k) => (o == null ? undefined : o[k]), obj);
}

async function readCurrent(rootDir, entry) {
  const raw = await readFile(path.join(rootDir, entry.file), 'utf8');
  const current = getPath(JSON.parse(raw), entry.path);
  if (typeof current !== 'string') {
    throw new Error(`set-version: ${entry.file} has no string at ${entry.path.join('.')}`);
  }
  return { raw, current };
}

/** Files whose stamped version differs from `version`: [{file, current}]. */
export async function checkVersion(rootDir, version) {
  assertSemver(version);
  const drift = [];
  for (const entry of VERSION_FILES) {
    const { current } = await readCurrent(rootDir, entry);
    if (current !== version) drift.push({ file: entry.file, current });
  }
  return drift;
}

/** Stamps `version` into every VERSION_FILES entry; returns the files changed. */
export async function setVersion(rootDir, version) {
  assertSemver(version);
  const changed = [];
  for (const entry of VERSION_FILES) {
    const { raw, current } = await readCurrent(rootDir, entry);
    if (current === version) continue;
    const needle = `"${entry.key}": "${current}"`;
    if (!raw.includes(needle)) {
      throw new Error(`set-version: ${entry.file} does not contain ${needle} verbatim`);
    }
    const next = raw.replace(needle, `"${entry.key}": "${version}"`);
    if (getPath(JSON.parse(next), entry.path) !== version) {
      throw new Error(`set-version: ${entry.file}: replacement did not land on ${entry.path.join('.')}`);
    }
    await writeFile(path.join(rootDir, entry.file), next);
    changed.push(entry.file);
  }
  return changed;
}

/** CLI entry: returns the process exit code. */
export async function main(argv, rootDir) {
  const check = argv.includes('--check');
  const [version] = argv.filter((a) => a !== '--check');
  if (!version) {
    console.error('usage: node scripts/set-version.mjs X.Y.Z [--check]');
    return 2;
  }
  try {
    if (check) {
      const drift = await checkVersion(rootDir, version);
      for (const d of drift) console.error(`set-version: ${d.file} is at ${d.current}, want ${version}`);
      if (drift.length) console.error(`Run \`bun run version:set ${version}\` and commit before tagging.`);
      return drift.length ? 1 : 0;
    }
    for (const file of await setVersion(rootDir, version)) console.log(`set-version: ${file} -> ${version}`);
    return 0;
  } catch (err) {
    console.error(err instanceof Error ? err.message : String(err));
    return 1;
  }
}

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = await main(process.argv.slice(2), ROOT);
}
