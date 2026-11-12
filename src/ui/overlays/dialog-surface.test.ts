import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

// specs/2026-11-03-narrow-windows-navigation.md N7.2: a dialog cannot float over the page without a panel. The kit's
// card is the panel of every dialog; a dialog whose content is the whole surface (`surface="content"`) paints it with
// an opaque background of its root class — checked here over the source of the product.

const root = resolve(__dirname, '../../..');

function sources(directory: string): string[] {
  return readdirSync(directory).flatMap((name) => {
    const path = join(directory, name);
    if (statSync(path).isDirectory()) return sources(path);
    return /\.tsx$/u.test(name) && !/\.test\.tsx$/u.test(name) ? [path] : [];
  });
}

/** The root class of the content of each `surface="content"` dialog: `module.class` and the file of its module. */
function contentRoots(file: string) {
  const text = readFileSync(file, 'utf8');
  const modules = new Map([...text.matchAll(/import (\w+) from '([^']+\.css)'/gu)].map((match) => [match[1]!, match[2]!]));
  return [...text.matchAll(/<Dialog\b(?:=>|[^>])*?\bsurface="content"(?:=>|[^>])*>\s*(?:\{(\w+)\}|<\w+(?:=>|[^>])*?className=\{(\w+)\.(\w+)\})/gu)].map((match) => {
    let [module, name] = [match[2], match[3]];
    // The content is a variable of the component: its first element names the class.
    if (match[1]) [, module, name] = text.match(new RegExp(`const ${match[1]} = \\(?\\s*<\\w+[^>]*?className=\\{(\\w+)\\.(\\w+)\\}`, 'u')) ?? [];
    return { file, name, css: module && modules.get(module) ? resolve(dirname(file), modules.get(module)!) : undefined };
  });
}

/** The declarations of the top-level rules that select `.name`, alone or in a list. */
function declarations(css: string, name: string) {
  const text = readFileSync(css, 'utf8');
  return [...text.matchAll(/(?:^|\n)([.\w\s,-]+)\{([^{}]*)\}/gu)]
    .filter((match) => match[1]!.split(',').some((selector) => selector.trim() === `.${name}`))
    .map((match) => match[2]!).join('\n');
}

describe('dialog surface', () => {
  const roots = sources(join(root, 'src')).flatMap(contentRoots);

  it('finds the dialogs whose content is the surface', () => {
    // The open sections alone have such dialogs (specs/2026-11-07-desktop-app.md DA16.5): the scan finds them. The questions
    // of the shelf went to the one confirmation (specs/2026-11-10-ux2-patterns-actions.md UC2), which draws the kit's card or
    // a sheet; the windows of the upload, of «Обновить книгу из файла» and of «Недавно удалённые» stay.
    expect(roots.length).toBeGreaterThanOrEqual(3);
    for (const found of roots) expect(found.css, `${found.file}: the root class of the content`).toBeDefined();
  });

  it.each(roots.map((found) => [`${found.file.slice(root.length + 1)} .${found.name}`, found] as const))(
    '%s paints an opaque background', (_, found) => {
      const background = declarations(found.css!, found.name!).match(/background(?:-color)?:\s*([^;]+);/u)?.[1]?.trim();
      expect(background, 'the background of the content root').toBeDefined();
      expect(background).not.toMatch(/^(transparent|none|inherit|initial|unset)$/u);
      expect(background).not.toMatch(/rgba?\([^)]*[,/]\s*0\s*\)$/u);
    });
});
