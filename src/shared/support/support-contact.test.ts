import { describe, expect, it } from 'vitest';
import { supportEmail, supportMailHref } from './support-contact';

function parse(href: string) {
  const url = new URL(href);
  return {
    address: url.pathname,
    body: url.searchParams.get('body') ?? '',
    protocol: url.protocol,
    subject: url.searchParams.get('subject') ?? '',
  };
}

describe('supportMailHref', () => {
  it('writes the code and the page path only, without the query string', () => {
    const letter = parse(supportMailHref('req-1', '/chat?projectId=p&chatId=c'));

    expect(letter.protocol).toBe('mailto:');
    expect(letter.address).toBe(supportEmail);
    expect(letter.subject).toBe('CanonKeeper: ошибка req-1');
    expect(letter.body).toContain('Код для поддержки: req-1');
    expect(letter.body).toContain('Страница: /chat');
    expect(letter.body).toContain('Что вы делали, когда появилась ошибка:');
    expect(letter.body).not.toContain('projectId');
    expect(letter.body).not.toContain('chatId');
  });

  it('asks a plain question without a code', () => {
    const letter = parse(supportMailHref(undefined, '/manuscript/books#top'));

    expect(letter.subject).toBe('CanonKeeper: вопрос');
    expect(letter.body.trim()).toBe('Страница: /manuscript/books');
  });

  it('treats a blank code as no code', () => {
    expect(parse(supportMailHref('  ', '/')).subject).toBe('CanonKeeper: вопрос');
  });
});
