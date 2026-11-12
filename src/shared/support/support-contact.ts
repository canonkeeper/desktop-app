/** The one support address the product names to a writer (R4.2.2). */
export const supportEmail = 'support@canonkeeper.ru';

/**
 * UC4.2 «Написать в поддержку»: a `mailto:` letter that carries only the support code and the
 * page path. The query string holds saga and chat ids, so it never goes into the letter (R4.2.2).
 */
export function supportMailHref(code: string | null | undefined, pathname: string): string {
  const page = pathname.split(/[?#]/)[0] || '/';
  const supportCode = code?.trim();
  const subject = supportCode ? `CanonKeeper: ошибка ${supportCode}` : 'CanonKeeper: вопрос';
  const body = supportCode
    ? `Код для поддержки: ${supportCode}\r\nСтраница: ${page}\r\n\r\nЧто вы делали, когда появилась ошибка:\r\n`
    : `Страница: ${page}\r\n`;
  return `mailto:${supportEmail}?subject=${encodeURIComponent(subject)}&body=${encodeURIComponent(body)}`;
}
