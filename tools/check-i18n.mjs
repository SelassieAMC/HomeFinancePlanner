#!/usr/bin/env node
// Translation parity check: every namespace bundle must carry the same key
// set under en, de, es, and interpolation placeholders must agree between
// languages ({{name}} in en must exist in de and es).
// Run from the repo root: node tools/check-i18n.mjs
import { build } from '../frontend/node_modules/esbuild/lib/main.js';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const NSES = [
  'common', 'auth', 'settings', 'dashboard', 'accounts', 'transactions',
  'bills', 'budgets', 'goals', 'bags', 'planning', 'schedules', 'products',
  'stores', 'cart', 'prompts',
];

// Transpile frontend/src into a temp dir, then import the bundles as ESM.
const outdir = mkdtempSync(join(tmpdir(), 'i18n-check-'));
await build({
  entryPoints: NSES.map((ns) => `frontend/src/i18n/locales/${ns}.ts`),
  outdir,
  bundle: true,
  format: 'esm',
  platform: 'node',
  external: ['i18next', 'react-i18next', 'react', 'react-dom'],
});

const flat = (obj, prefix = '', out = {}) => {
  for (const [k, v] of Object.entries(obj ?? {})) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === 'object' && !Array.isArray(v)) flat(v, key, out);
    else out[key] = v;
  }
  return out;
};
const placeholders = (s) =>
  [...String(s).matchAll(/{{(\w+)}}/g)].map((m) => m[1]).sort();

let failures = 0;
for (const ns of NSES) {
  // Entry file name mirrors the source path: locales/<ns>.ts
  const mod = await import(pathToFileURL(join(outdir, ns === ns ? ns : '') + '.js'));
  const bundle = mod.default ?? mod[Object.keys(mod)[0]];
  const langs = { en: flat(bundle.en), de: flat(bundle.de), es: flat(bundle.es) };
  const en = langs.en;
  for (const lng of ['de', 'es']) {
    const missing = Object.keys(en).filter((k) => !(k in langs[lng]));
    const extra = Object.keys(langs[lng]).filter((k) => !(k in en));
    if (missing.length || extra.length) {
      failures++;
      console.error(`[${ns}] key parity en↔${lng}:`);
      if (missing.length) console.error(`  missing in ${lng}:`, missing);
      if (extra.length) console.error(`  unexpected in ${lng}:`, extra);
    }
    const phMismatch = Object.entries(en).filter(([k, v]) => {
      if (!(k in langs[lng])) return false;
      return JSON.stringify(placeholders(v)) !== JSON.stringify(placeholders(langs[lng][k]));
    });
    if (phMismatch.length) {
      failures++;
      console.error(`[${ns}] placeholder mismatch en↔${lng}:`, phMismatch.map(([k]) => k));
    }
  }
  console.log(`[${ns}] ${Object.keys(en).length} keys ✓`);
}

if (failures) {
  console.error(`\ni18n parity check FAILED (${failures} problem groups)`);
  process.exit(1);
}
console.log('\ni18n parity check OK');