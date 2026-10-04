import { mkdtemp, readFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';
import { describe, expect, it } from 'vitest';

import { OUTPUTS, ROOT, SOURCE_SVG, deriveVariants, renderAll } from './gen-mobile-assets.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SLOW = { timeout: 60_000 };

async function rgba(buf, x, y) {
  const { data, info } = await sharp(buf).ensureAlpha().raw().toBuffer({ resolveWithObject: true });
  const i = (y * info.width + x) * info.channels;
  return [data[i], data[i + 1], data[i + 2], data[i + 3]];
}

function byName(name) {
  const spec = OUTPUTS.find((o) => path.basename(o.file) === name);
  if (!spec) throw new Error(`no output named ${name}`);
  return spec;
}

describe('gen-mobile-assets', () => {
  it('ROOT is the repo root and the source SVG exists there', async () => {
    expect(path.resolve(HERE, '..')).toBe(ROOT);
    const svg = await readFile(path.join(ROOT, SOURCE_SVG), 'utf8');
    expect(svg).toContain('rx="96" fill="#0a0a0a"');
  });

  it('deriveVariants squares the background and strips it for the glyph', async () => {
    const svg = await readFile(path.join(ROOT, SOURCE_SVG), 'utf8');
    const v = deriveVariants(svg);
    expect(v.original).toBe(svg);
    expect(v.square).toContain('rx="0" fill="#0a0a0a"');
    expect(v.square).not.toContain('rx="96"');
    expect(v.glyph('#ffffff')).not.toContain('#0a0a0a');
    expect(v.glyph('#ffffff')).not.toContain('#fafafa');
    expect(v.glyph('#ffffff')).toContain('stroke="#ffffff"');
  });

  // Review Focus 5: a reshaped source must fail loudly, not emit blank icons.
  it('deriveVariants throws when the leading background rect is missing or recolored', () => {
    const noRect = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><circle r="10" fill="#fafafa"/></svg>';
    expect(() => deriveVariants(noRect)).toThrow(/expected a leading #0a0a0a background <rect/);
    const wrongFill = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><rect width="512" height="512" rx="96" fill="#ffffff"/></svg>';
    expect(() => deriveVariants(wrongFill)).toThrow(/expected a leading #0a0a0a background <rect/);
  });

  // A recolored glyph would make glyph(color) a silent no-op (white-on-white splash).
  it('deriveVariants throws when the glyph color is missing from the source', () => {
    const recolored =
      '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><rect width="512" height="512" rx="96" fill="#0a0a0a"/><path d="M0 0" stroke="#ff0000"/></svg>';
    expect(() => deriveVariants(recolored)).toThrow(/glyph color #fafafa/);
  });

  it('deriveVariants throws when the background rect is no longer first', () => {
    const glyphFirst =
      '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><rect x="10" y="10" width="20" height="20" stroke="#fafafa"/><rect width="512" height="512" rx="96" fill="#0a0a0a"/></svg>';
    expect(() => deriveVariants(glyphFirst)).toThrow(/expected a leading #0a0a0a background <rect/);
  });

  it('renders every output at its documented size with the right alpha', SLOW, async () => {
    const outDir = await mkdtemp(path.join(os.tmpdir(), 'calendium-assets-'));
    const written = await renderAll({ outDir });
    expect(written).toEqual(OUTPUTS.map((o) => o.file));

    for (const spec of OUTPUTS) {
      const meta = await sharp(path.join(outDir, spec.file)).metadata();
      expect([spec.file, meta.width, meta.height]).toEqual([spec.file, spec.size, spec.size]);
    }

    const icon = await readFile(path.join(outDir, byName('icon.png').file));
    expect((await sharp(icon).metadata()).hasAlpha).toBe(false); // App Store rejects alpha
    expect(await rgba(icon, 2, 2)).toEqual([10, 10, 10, 255]); // square corner is background

    const adaptive = await readFile(path.join(outDir, byName('adaptive-icon.png').file));
    expect((await rgba(adaptive, 2, 2))[3]).toBe(0); // transparent outside the glyph
    expect(await rgba(adaptive, 482, 556)).toEqual([250, 250, 250, 255]); // glyph bar, 60% scale

    const splash = await readFile(path.join(outDir, byName('splash-icon.png').file));
    expect((await rgba(splash, 2, 2))[3]).toBe(0);
    expect(await rgba(splash, 462, 586)).toEqual([10, 10, 10, 255]);

    const splashDark = await readFile(path.join(outDir, byName('splash-icon-dark.png').file));
    expect(await rgba(splashDark, 462, 586)).toEqual([250, 250, 250, 255]);

    const notif = await readFile(path.join(outDir, byName('notification-icon.png').file));
    const { data, info } = await sharp(notif).ensureAlpha().raw().toBuffer({ resolveWithObject: true });
    expect(info.width).toBe(96);
    let visible = 0;
    for (let i = 0; i < data.length; i += info.channels) {
      if (data[i + 3] === 0) continue;
      visible += 1;
      // Android tints the notification glyph: every visible pixel must be white.
      expect(data[i]).toBeGreaterThanOrEqual(250);
      expect(data[i + 1]).toBeGreaterThanOrEqual(250);
      expect(data[i + 2]).toBeGreaterThanOrEqual(250);
    }
    expect(visible).toBeGreaterThan(200);

    const appicon = await readFile(path.join(outDir, byName('appicon.png').file));
    expect((await rgba(appicon, 0, 0))[3]).toBe(0); // rounded corners stay transparent
    expect(await rgba(appicon, 512, 512)).toEqual([10, 10, 10, 255]);
  });

  it('regenerating over the committed assets is byte-identical (no drift)', SLOW, async () => {
    const outDir = await mkdtemp(path.join(os.tmpdir(), 'calendium-assets-'));
    await renderAll({ outDir });
    for (const spec of OUTPUTS) {
      const fresh = await readFile(path.join(outDir, spec.file));
      const committed = await readFile(path.join(ROOT, spec.file));
      expect(fresh.equals(committed), `${spec.file} drifted: run \`bun run assets:gen\` and commit`).toBe(true);
    }
  });
});
