import { readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import '../styles/fonts.css';
import '../styles/tokens.css';
import '../styles/base.css';

/** The Latin and Cyrillic subset the fonts keep (specs/2026-10-28-writer-first-success.md, UC6 Data). */
const fontSubset = [
  'U+0000-00FF', 'U+0131', 'U+0152-0153', 'U+02BB-02BC', 'U+02C6', 'U+02DA', 'U+02DC',
  'U+0400-045F', 'U+0490-0491', 'U+04B0-04B1', 'U+2000-206F',
  'U+20BD', 'U+2116', 'U+2122', 'U+2190-2193', 'U+2212', 'U+2215', 'U+2248', 'U+2260', 'U+2264-2265',
];
/** The four variable TTF files the app shipped before the subsets: 3 128 724 bytes. */
const ttfBytes = 3_128_724;

function rootToken(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

describe('UIKit foundations', () => {
  it('disables automatic optical sizing at the shared inheritance root', () => {
    expect(getComputedStyle(document.documentElement).getPropertyValue('font-optical-sizing')).toBe('none');
  });

  it('exposes the canonical fonts, colors, spacing, radii and focus color', () => {
    expect(rootToken('--ck-font-sans')).toBe('Inter, system-ui, sans-serif');
    expect(rootToken('--ck-font-serif')).toBe('"Source Serif 4", Georgia, serif');
    expect(rootToken('--ck-font-mono')).toBe('"JetBrains Mono", "SFMono-Regular", Consolas, monospace');
    expect(rootToken('--ck-primary-hover')).toBe('#4338ca');
    expect(rootToken('--ck-muted-foreground')).toBe('#5F6673');
    expect(rootToken('--ck-sidebar-foreground')).toBe('#5F6673');
    expect(rootToken('--ck-link')).toBe('#4f46e5');
    expect(rootToken('--ck-destructive-text')).toBe('#dc2626');
    expect(rootToken('--ck-chat-user-message')).toBe('#5442c5');
    expect(rootToken('--ck-control-hover-border')).toBe('#c9c9c4');
    expect(rootToken('--ck-scrim')).toBe('#17182859');
    expect(getComputedStyle(document.body).color).toBe('var(--ck-foreground)');
    expect(rootToken('--ck-space-8')).toBe('64px');
    expect(rootToken('--ck-radius-control')).toBe('9px');
    expect(rootToken('--ck-focus-shadow')).toBe('#4f46e566');
  });

  it('loads only the three local font families', () => {
    const fontFaces = [...document.styleSheets]
      .flatMap((sheet) => [...sheet.cssRules])
      .filter((rule): rule is CSSFontFaceRule => rule.type === CSSRule.FONT_FACE_RULE);

    expect(fontFaces.map((rule) => rule.style.getPropertyValue('font-family'))).toEqual([
      'Inter',
      '"Source Serif 4"',
      '"Source Serif 4"',
      '"JetBrains Mono"',
    ]);
    expect(fontFaces.every((rule) => !/https?:\/\//u.test(rule.style.getPropertyValue('src')))).toBe(true);
  });

  it('loads Latin and Cyrillic WOFF2 subsets, three times lighter than the TTF fonts (UC6, R6.1-R6.3)', () => {
    const fontFaces = [...document.styleSheets]
      .flatMap((sheet) => [...sheet.cssRules])
      .filter((rule): rule is CSSFontFaceRule => rule.type === CSSRule.FONT_FACE_RULE);
    const fontFiles = readdirSync(join(process.cwd(), 'src/assets/fonts'), { recursive: true, encoding: 'utf8' })
      .filter((file) => /\.(ttf|otf|woff2?)$/u.test(file));

    for (const rule of fontFaces) {
      expect(rule.style.getPropertyValue('src')).toMatch(/^url\(["']?[^"')]+\.woff2["']?\) format\(["']?woff2["']?\)$/u);
      expect(rule.style.getPropertyValue('unicode-range').split(/,\s*/u)).toEqual(fontSubset);
      expect(rule.style.getPropertyValue('font-display')).toBe('swap');
    }
    expect(fontFiles.every((file) => file.endsWith('.woff2'))).toBe(true);
    expect(fontFiles).toHaveLength(fontFaces.length);
    const bytes = fontFiles.reduce((total, file) => total + statSync(join(process.cwd(), 'src/assets/fonts', file)).size, 0);
    expect(bytes * 3).toBeLessThanOrEqual(ttfBytes);
  });

  it('removes animation and transition motion when requested', () => {
    const reducedMotion = [...document.styleSheets]
      .flatMap((sheet) => [...sheet.cssRules])
      .find((rule) => rule instanceof CSSMediaRule && rule.conditionText === '(prefers-reduced-motion: reduce)');

    expect(reducedMotion?.cssText).toContain('animation-duration: 0.01ms !important');
    expect(reducedMotion?.cssText).toContain('transition-duration: 0.01ms !important');
  });
});
