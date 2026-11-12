import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

/**
 * The sets of colours of tokens.css (specs/2026-11-12-ux11-dark-core.md TH2.1): every colour of the light set has its dark
 * value, the dark set names nothing the light one does not, and the plain `:root` holds no colour — a colour added there
 * would have no dark value and would stay light on a dark site.
 */
const tokens = readFileSync(path.join(process.cwd(), 'src/styles/tokens.css'), 'utf8');

/** The blocks of the file: their selectors and the custom properties they declare. */
function blocks(css: string) {
  const plain = css.replace(/\/\*[\s\S]*?\*\//gu, '');
  return [...plain.matchAll(/([^{}]+)\{([^{}]*)\}/gu)].map(([, selector, body]) => ({
    selector: selector!.replace(/\s+/gu, ' ').trim(),
    properties: new Map([...body!.matchAll(/(--ck-[\w-]+)\s*:\s*([^;]+);/gu)].map(([, name, value]) => [name!, value!.trim()])),
    scheme: /color-scheme\s*:\s*(\w+)/u.exec(body!)?.[1] ?? null,
  }));
}

const all = blocks(tokens);
const light = all.find((block) => block.selector.includes("[data-reading-theme='light']"));
const dark = all.find((block) => block.selector.includes("[data-theme='dark']"));
const plainRoot = all.find((block) => block.selector === ':root');
const colour = /#[0-9a-f]{3,8}\b|rgba?\(|hsla?\(|color-mix\(/iu;

describe('the colours of the site', () => {
  it('has a light set for the site and the light sheets, and a dark set for the dark site and the dark sheets', () => {
    expect(light?.selector).toBe(":root, [data-reading-theme='light'], [data-reading-theme='sepia']");
    expect(dark?.selector).toBe(":root[data-theme='dark'], [data-reading-theme='dark']");
    expect(light?.scheme).toBe('light');
    expect(dark?.scheme).toBe('dark');
  });

  it('gives every colour of the light set its dark value, and the dark set no colour of its own', () => {
    const lightNames = [...light!.properties.keys()].sort();
    const darkNames = [...dark!.properties.keys()].sort();
    expect(lightNames.length).toBeGreaterThan(70);
    expect(lightNames.filter((name) => !dark!.properties.has(name))).toEqual([]);
    expect(darkNames.filter((name) => !light!.properties.has(name))).toEqual([]);
    for (const [name, value] of light!.properties) expect([name, colour.test(value)]).toEqual([name, true]);
  });

  it('keeps no colour in the plain :root', () => {
    expect([...plainRoot!.properties].filter(([, value]) => colour.test(value))).toEqual([]);
  });
});
