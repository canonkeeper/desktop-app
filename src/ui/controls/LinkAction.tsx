import type { ComponentPropsWithRef } from 'react';
import styles from './controls.module.css';

export type LinkActionProps =
  | (ComponentPropsWithRef<'a'> & { href: string })
  | (Omit<ComponentPropsWithRef<'button'>, 'type'> & { href?: undefined });

/**
 * An action written as a link inside a line — a chapter of the card of a node, «Ваши материалы» at «Добавить книги»
 * (specs/2026-11-11-ux9-shelf-chat.md SC2.1, П-16): it reads as a word of the line, and under a finger and below 900 px its
 * target is 44 px by the control, not by the page. With `href` it is a link the browser may open in a tab.
 */
export function LinkAction(props: LinkActionProps) {
  const className = `${styles.linkAction}${props.className ? ` ${props.className}` : ''}`;
  if (props.href !== undefined) {
    const { className: _own, ...link } = props;
    return <a {...link} className={className} />;
  }
  const { className: _own, href: _none, ...button } = props;
  return <button {...button} className={className} type="button" />;
}
