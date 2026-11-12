import { X } from 'lucide-react';
import { type FormEvent, useEffect, useRef, useState, type RefObject } from 'react';
import type { ChapterNavigationLocator, ManuscriptApiClient, SearchResult, SearchTextCoverage, SourceView } from '../../entities/manuscript/api';
import { publicApiErrorMessage } from '../../entities/api-errors';
import { useChapterSearch } from './api/chapter-search';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { SearchField } from '../../ui/controls/SearchField';
import { Alert } from '../../ui/feedback/Alert';
import { SupportReference } from '../../ui/feedback/SupportReference';
import { SegmentedControl } from '../../ui/navigation/SegmentedControl';
import { Overlay } from '../../ui/overlays/Overlay';
import { Portal } from '../../ui/overlays/Portal';
import type { PresenceStatus } from '../../ui/overlays/use-presence';
import { pluralize } from '../../ui/copy';
import styles from './SearchPanel.module.css';
import { EmptyFilter } from '../../ui/feedback/EmptyState';
import { SkeletonArea } from '../../ui/feedback/Skeleton';
import { useKeyboardAtHand } from '../../ui/use-narrow-screen';

/** How long the words rest before the search goes (specs/2026-11-11-ux9-shelf-chat.md SC2.3): a pause of the typing. */
export const searchPauseMs = 350;

export type SearchScope = 'book' | 'project';
export type SearchPanelStatus = 'loading' | 'ready' | 'empty' | 'error';

/** «Поиск идёт по 1 из 2 книг»: the books whose text the search reads now; the others the helper still reads. */
function booksSearched(coverage: SearchTextCoverage) {
  return `Поиск идёт по ${coverage.currentBooks} из ${coverage.totalBooks} ${pluralize(coverage.totalBooks, 'книги', 'книг', 'книг')}`;
}

interface SearchPanelProps {
  api: Pick<ManuscriptApiClient, 'searchChapters'>;
  onOpenChapter: (locator: ChapterNavigationLocator) => void;
  bookName?: string;
  bookScopeDisabled?: boolean;
  coverage: SearchTextCoverage | null;
  onClose: () => void;
  onOpenResult: (result: SearchResult) => void;
  onRunSearch: (event?: FormEvent<HTMLFormElement>) => void;
  results: SearchResult[];
  returnFocusRef?: RefObject<HTMLElement | null>;
  scope: SearchScope;
  searchError: string;
  searchRequestId?: string;
  searchStatus: SearchPanelStatus;
  searchTerm: string;
  setScope: (scope: SearchScope) => void;
  setSearchTerm: (value: string) => void;
  sourceView: SourceView;
  state: PresenceStatus;
  title: string;
}

export function SearchPanel({
  api,
  onOpenChapter,
  bookName = '',
  bookScopeDisabled = false,
  coverage,
  onClose,
  onOpenResult,
  onRunSearch,
  results,
  returnFocusRef,
  scope,
  searchError,
  searchRequestId,
  searchStatus,
  searchTerm,
  setScope,
  setSearchTerm,
  sourceView,
  state,
  title,
}: SearchPanelProps) {
  const searchInputRef = useRef<HTMLInputElement>(null);
  const [submittedQuery, setSubmittedQuery] = useState(searchTerm.trim());
  const chapters = useChapterSearch(api, submittedQuery, sourceView);
  const chapterResults = chapters.data?.pages.flatMap((page) => page.data) ?? [];
  const chaptersLoading = Boolean(submittedQuery) && chapters.isPending;
  const incompleteCoverage = coverage
    ? coverage.currentBooks < coverage.totalBooks || coverage.nonCurrentBooks > 0 || coverage.truncated
    : false;
  const visibleResults = searchStatus === 'ready' ? results : [];
  const resultCount = visibleResults.length + chapterResults.length;
  const keyboard = useKeyboardAtHand();
  // The search goes as the words are typed, after a pause of the typing (SC2.3, W-5I); Enter and «Найти» go at once.
  const runRef = useRef(onRunSearch);
  runRef.current = onRunSearch;
  useEffect(() => {
    const query = searchTerm.trim();
    if (!query || query === submittedQuery) return undefined;
    const timer = window.setTimeout(() => {
      setSubmittedQuery(query);
      runRef.current();
    }, searchPauseMs);
    return () => window.clearTimeout(timer);
  }, [searchTerm, submittedQuery]);

  function runSearch(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault();
    // «Найти» waits disabled while the books are searched: the focus goes back to the field instead of dropping out of the
    // window, so Escape still closes it and the next words go to the field.
    if (searchInputRef.current && document.activeElement?.matches('button[type="submit"]')) searchInputRef.current.focus();
    const query = searchTerm.trim();
    if (query && query === submittedQuery) void chapters.refetch();
    setSubmittedQuery(query);
    onRunSearch();
  }

  function moveResultFocus(event: React.KeyboardEvent<HTMLDivElement>) {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    const origin = event.target as HTMLElement;
    if (origin.closest('input, select, textarea, [role="combobox"], [role="listbox"], [aria-haspopup="listbox"]')) return;
    const resultButtons = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('[data-search-result-action]')];
    if (resultButtons.length === 0) return;
    event.preventDefault();
    const currentIndex = resultButtons.indexOf(document.activeElement as HTMLButtonElement);
    const nextIndex = currentIndex < 0
      ? event.key === 'ArrowDown' ? 0 : resultButtons.length - 1
      : Math.max(0, Math.min(resultButtons.length - 1, currentIndex + (event.key === 'ArrowDown' ? 1 : -1)));
    resultButtons[nextIndex]?.focus();
  }

  return (
    <Portal>
      <div
        className={styles.layer}
        data-state={state}
        onPointerDown={(event) => event.target === event.currentTarget && onClose()}
      >
        <Overlay
          aria-busy={searchStatus === 'loading'}
          className={styles.panel}
          kind="dialog"
          initialFocusRef={searchInputRef}
          label={title}
          onDismiss={onClose}
          onKeyDown={moveResultFocus}
          returnFocusRef={returnFocusRef}
          state={state}
        >
          <div className={styles.titleRow}>
            <div>
              <h2>{title}</h2>
              <p>
                {scope === 'book' && bookName ? `Книга · ${bookName}` : 'Вся сага'}
              </p>
            </div>
            <IconButton aria-label="Закрыть поиск" onClick={onClose}>
              <X aria-hidden="true" size={18} />
            </IconButton>
          </div>
          <form className={styles.form} onSubmit={runSearch}>
            <SearchField
              autoFocus
              label={<span className={styles.srOnly}>Запрос поиска</span>}
              inputRef={searchInputRef}
              loading={searchStatus === 'loading'}
              onChange={(event) => { setSearchTerm(event.target.value); if (!event.target.value.trim()) setSubmittedQuery(''); }}
              onClear={() => { setSearchTerm(''); setSubmittedQuery(''); }}
              placeholder="Название, номер главы или текст…"
              value={searchTerm}
            />
            <SegmentedControl
              label="Область поиска по тексту"
              onValueChange={(value) => { setSubmittedQuery(searchTerm.trim()); setScope(value); }}
              options={[
                { disabled: bookScopeDisabled, label: 'Книга', value: 'book' },
                { label: 'Вся сага', value: 'project' },
              ]}
              value={scope}
            />
            <Button className={styles.submit} loading={searchStatus === 'loading'} size="sm" type="submit" variant="primary">Найти</Button>
          </form>
          <div aria-label="Результаты поиска" role="region" className={styles.results} onScroll={(event) => {
            const list = event.currentTarget;
            if (chapters.hasNextPage && !chapters.isFetching && !chapters.isFetchNextPageError && list.scrollHeight - list.scrollTop - list.clientHeight < 120) void chapters.fetchNextPage({ cancelRefetch: false });
          }}>
            {incompleteCoverage ? (
              <Alert className={styles.coverageWarning} tone="warning">
                <strong>{coverage ? booksSearched(coverage) : null}</strong>
                <span>Остальные книги помощник ещё читает — результаты могут быть неполными.</span>
                {coverage?.truncated ? <span>Список результатов усечён.</span> : null}
              </Alert>
            ) : null}
            {searchStatus === 'ready' && resultCount === 0 && !submittedQuery ? (
              <p className={styles.muted}>Введите название, номер главы или слова из текста.</p>
            ) : null}
            {searchStatus === 'loading' || chaptersLoading ? <p className={styles.muted} role="status">Ищем…</p> : null}
            {(searchStatus === 'empty' || submittedQuery && chapters.isSuccess && searchStatus === 'ready') && resultCount === 0 && !chaptersLoading && !chapters.isError ? (
              // Nothing found is not an empty saga (specs/2026-11-10-ux2-patterns-states.md PS2.2, PS2.3): one way on — the
              // whole saga from a book, another word from the saga.
              <EmptyFilter onReset={() => {
                if (scope === 'book') { setSubmittedQuery(searchTerm.trim()); setScope('project'); return; }
                setSearchTerm('');
                setSubmittedQuery('');
              }} reset={scope === 'book' ? 'Искать во всей саге' : 'Очистить поиск'} words={`Ничего не нашлось по «${searchTerm.trim()}»`} />
            ) : null}
            {searchStatus === 'error' ? (
              <Alert className={styles.state} tone="error">
                <strong>Поиск по книгам недоступен</strong>
                <span>{searchError}</span>
                {searchRequestId ? <SupportReference code={searchRequestId} /> : null}
                <Button onClick={() => onRunSearch()} size="sm">Повторить поиск</Button>
              </Alert>
            ) : null}
            {chapters.isError && !chapters.isFetchNextPageError ? <Alert tone="error">
              <strong>Поиск глав недоступен</strong>
              <span>{publicApiErrorMessage(chapters.error, 'Попробуйте ещё раз.')}</span>
              <Button onClick={() => void chapters.refetch()} size="sm">Повторить поиск глав</Button>
            </Alert> : null}
            {chapterResults.map((result) => <article className={styles.result} key={`chapter:${result.locator.chapterId}`}>
              <strong>{result.chapterTitle.startsWith(`Глава ${result.chapterNumber}.`) ? result.chapterTitle : `Глава ${result.chapterNumber} · ${result.chapterTitle}`}</strong>
              <span>{result.projectTitle} · {result.bookTitle}</span>
              <div className={styles.resultActions}><Button data-search-result-action onClick={() => onOpenChapter(result.locator)} size="sm">Открыть главу</Button></div>
            </article>)}
            {chapters.isFetchingNextPage ? <SkeletonArea count={2} shape="rows" /> : null}
            {chapters.isFetchNextPageError ? <Alert tone="error">
              <span>Не удалось загрузить следующие главы. Уже найденные результаты сохранены.</span>
              <Button onClick={() => void chapters.fetchNextPage()} size="sm">Повторить загрузку глав</Button>
            </Alert> : null}
            {visibleResults.map((result) => (
              <article className={styles.result} key={result.id}>
                <strong>{result.title}</strong>
                <span>Совпадение в тексте</span>
                <p>{result.excerpt}</p>
                <div className={styles.resultActions}>
                  <Button data-search-result-action onClick={() => onOpenResult(result)} size="sm">Открыть абзац</Button>
                </div>
              </article>
            ))}
          </div>
          <footer className={styles.footer}>
            <span>
              {resultCount > 0
                ? `${resultCount} ${pluralize(resultCount, 'результат', 'результата', 'результатов')}${coverage ? ` · ${booksSearched(coverage).toLowerCase()}` : ''}`
                : null}
            </span>
            {/* The keys are named only where there is a keyboard (SC2.3, П-19). */}
            {keyboard ? <span>Esc — закрыть · ↑↓ — по результатам</span> : null}
          </footer>
        </Overlay>
      </div>
    </Portal>
  );
}
