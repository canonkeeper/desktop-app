import { X } from 'lucide-react';
import { type FormEvent, useRef, useState, type RefObject } from 'react';
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

export type SearchScope = 'book' | 'project';
export type SearchPanelStatus = 'loading' | 'ready' | 'empty' | 'error';

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
  variant?: 'reader' | 'shelf';
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
  variant = 'reader',
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

  function runSearch(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault();
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
        data-variant={variant}
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
          <p className={styles.muted}>Главы — во всех доступных сагах, даже до подготовки. Область выше — для текста.</p>
          <div aria-label="Результаты поиска" role="region" className={styles.results} onScroll={(event) => {
            const list = event.currentTarget;
            if (chapters.hasNextPage && !chapters.isFetching && !chapters.isFetchNextPageError && list.scrollHeight - list.scrollTop - list.clientHeight < 120) void chapters.fetchNextPage({ cancelRefetch: false });
          }}>
            {incompleteCoverage ? (
              <Alert className={styles.coverageWarning} tone="warning">
                <strong>Покрытие неполное · {coverage?.currentBooks} из {coverage?.totalBooks}</strong>
                <span>Часть текста ещё готовится. Результаты могут быть неполными.</span>
                {coverage?.truncated ? <span>Список результатов усечён.</span> : null}
              </Alert>
            ) : null}
            {searchStatus === 'ready' && resultCount === 0 && !submittedQuery ? (
              <p className={styles.muted}>Введите название, номер главы или фрагмент текста.</p>
            ) : null}
            {searchStatus === 'loading' || chaptersLoading ? <p className={styles.muted} role="status">Ищем…</p> : null}
            {(searchStatus === 'empty' || submittedQuery && chapters.isSuccess && searchStatus === 'ready') && resultCount === 0 && !chaptersLoading && !chapters.isError ? (
              <div className={styles.state}>
                <strong>Ничего не найдено по «{searchTerm.trim()}»</strong>
                <span>Попробуйте другую форму слова или область «Вся сага».</span>
              </div>
            ) : null}
            {searchStatus === 'error' ? (
              <Alert className={styles.state} tone="error">
                <strong>Поиск по тексту недоступен</strong>
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
            {chapters.isFetchingNextPage ? <p className={styles.muted} role="status">Загружаем главы…</p> : null}
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
                ? `${resultCount} ${pluralize(resultCount, 'результат', 'результата', 'результатов')}${coverage ? ` · текст: покрытие ${coverage.currentBooks} из ${coverage.totalBooks}` : ''}`
                : null}
            </span>
            <span>Enter — искать · Esc — закрыть · ↑↓ — по результатам</span>
          </footer>
        </Overlay>
      </div>
    </Portal>
  );
}
