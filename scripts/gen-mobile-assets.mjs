#!/usr/bin/env node
// scripts/gen-mobile-assets.mjs
//
// Derives every raster app icon from the one vector source of truth,
// apps/web/public/icon.svg (512 viewBox: a #0a0a0a rounded square rx=96
// carrying a #fafafa calendar glyph; already the web favicon). Outputs are
// committed and builds never run this; scripts/gen-mobile-assets.test.mjs
// regenerates into a temp dir and fails on drift. Edit the SVG, run
// `bun run assets:gen`, commit the PNGs together.
//
// Run with node (sharp's native binding): `node scripts/gen-mobile-assets.mjs`.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';

export const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const SOURCE_SVG = 'apps/web/public/icon.svg';

const SOURCE_VIEWBOX = 512;
const BACKGROUND = '#0a0a0a';
const GLYPH = '#fafafa';
const TRANSPARENT = { r: 0, g: 0, b: 0, alpha: 0 };
// Deterministic encoder settings so regeneration is byte-identical.
const PNG_OPTIONS = { compressionLevel: 9, adaptiveFiltering: false };

/**
 * Every file the script writes, relative to the repo root.
 *  - original: the SVG as-is (rounded square, transparent corners)
 *  - square:   background rx forced to 0 and flattened (App Store icons must be opaque)
 *  - glyph:    background removed, glyph recolored to `color`, optionally scaled
 *              inside the canvas (`scale`) on a transparent background
 */
export const OUTPUTS = [
  { file: 'apps/mobile/assets/images/icon.png', size: 1024, variant: 'square' },
  { file: 'apps/mobile/assets/images/adaptive-icon.png', size: 1024, variant: 'glyph', color: GLYPH, scale: 0.6 },
  { file: 'apps/mobile/assets/images/splash-icon.png', size: 1024, variant: 'glyph', color: BACKGROUND },
  { file: 'apps/mobile/assets/images/splash-icon-dark.png', size: 1024, variant: 'glyph', color: GLYPH },
  { file: 'apps/mobile/assets/images/notification-icon.png', size: 96, variant: 'glyph', color: '#ffffff' },
  { file: 'apps/mobile/assets/images/favicon.png', size: 48, variant: 'original' },
  { file: 'apps/desktop/build/appicon.png', size: 1024, variant: 'original' },
];

const FIRST_RECT = /<rect\b[^>]*\/>/;

/** Splits the source SVG into the strings each variant is rendered from. */
export function deriveVariants(svg) {
  const background = FIRST_RECT.exec(svg)?.[0];
  if (!background?.includes(`fill="${BACKGROUND}"`) || !/\brx="\d+"/.test(background)) {
    throw new Error(
      `${SOURCE_SVG}: expected a leading ${BACKGROUND} background <rect .../> with rx before the glyph`
    );
  }
  const square = svg.replace(background, background.replace(/\brx="\d+"/, 'rx="0"'));
  const glyphBase = svg.replace(background, '');
  if (!glyphBase.includes(GLYPH)) {
    // glyph(color) recolors by replacing GLYPH; without it every glyph variant
    // would silently keep the source color (e.g. a white-on-white splash).
    throw new Error(`${SOURCE_SVG}: expected the glyph color ${GLYPH} after the background <rect .../>`);
  }
  return {
    original: svg,
    square,
    glyph: (color) => glyphBase.split(GLYPH).join(color),
  };
}

function load(svg, size) {
  // Render the vector at the target size (density hint) instead of upscaling a
  // 512 px raster, then pin the exact pixel dimensions.
  return sharp(Buffer.from(svg), { density: (72 * size) / SOURCE_VIEWBOX }).resize(size, size);
}

/** Renders one OUTPUTS entry to a PNG buffer. */
export async function renderOutput(variants, spec) {
  const { size } = spec;
  if (spec.variant === 'original') return load(variants.original, size).png(PNG_OPTIONS).toBuffer();
  if (spec.variant === 'square') {
    return load(variants.square, size).flatten({ background: BACKGROUND }).png(PNG_OPTIONS).toBuffer();
  }
  const scale = spec.scale ?? 1;
  const inner = Math.round(size * scale);
  const glyph = await load(variants.glyph(spec.color), inner).png().toBuffer();
  const before = Math.floor((size - inner) / 2);
  const after = size - inner - before;
  return sharp(glyph)
    .extend({ top: before, bottom: after, left: before, right: after, background: TRANSPARENT })
    .png(PNG_OPTIONS)
    .toBuffer();
}

/** Writes every OUTPUTS entry under outDir (default: the repo). Returns the files written. */
export async function renderAll({ sourceSvg = path.join(ROOT, SOURCE_SVG), outDir = ROOT } = {}) {
  const variants = deriveVariants(await readFile(sourceSvg, 'utf8'));
  const written = [];
  for (const spec of OUTPUTS) {
    const abs = path.join(outDir, spec.file);
    await mkdir(path.dirname(abs), { recursive: true });
    await writeFile(abs, await renderOutput(variants, spec));
    written.push(spec.file);
  }
  return written;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  for (const file of await renderAll()) console.log(`assets:gen ${file}`);
}
