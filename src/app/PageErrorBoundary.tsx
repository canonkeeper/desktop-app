import { Component, type ReactNode } from 'react';
import { ClientErrorScreen } from './ClientErrorScreen';

interface PageErrorBoundaryProps {
  children: ReactNode;
}

/**
 * An error of a page inside the shell (specs/2026-11-11-ux9-account.md AC3.2): the card «Что-то пошло не так» takes the
 * place of the page, the shell and the window «Написать нам» of the account stay — so «Написать нам» of the card opens it
 * with the code. The root gives a new boundary to each page by its key; an error outside the pages stays the router's.
 */
export class PageErrorBoundary extends Component<PageErrorBoundaryProps, { error: unknown }> {
  override state: { error: unknown } = { error: undefined };

  static getDerivedStateFromError(error: unknown) {
    return { error: error ?? new Error('render') };
  }

  override render() {
    return this.state.error === undefined ? this.props.children : <ClientErrorScreen error={this.state.error} inShell />;
  }
}
