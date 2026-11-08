import { BookOpen, Info, RefreshCw, ShieldX, TriangleAlert } from 'lucide-react';
import { Button } from '../../../ui/controls/Button';
import { Alert } from '../../../ui/feedback/Alert';
import { Skeleton } from '../../../ui/feedback/Skeleton';
import { SupportReference } from '../../../ui/feedback/SupportReference';
import styles from './ReaderStates.module.css';
import type { ReaderSourceProblem } from './reader-locator';

export function ReaderStatusBanner({ message, onRetry, supportCode, tone = 'neutral' }: { message: string; onRetry?: () => void; supportCode?: string; tone?: 'neutral' | 'error' }) {
  if (tone === 'error') {
    return (
      <Alert className={styles.errorState} tone="error">
        <TriangleAlert aria-hidden="true" size={20} />
        <span>
          <strong>Не удалось открыть главу</strong>
          <span>{message}</span>
          {supportCode ? <SupportReference code={supportCode} /> : null}
        </span>
        {onRetry ? <Button onClick={onRetry} size="sm"><RefreshCw aria-hidden="true" size={15} />Повторить</Button> : null}
      </Alert>
    );
  }
  return (
    <div className={styles.banner} role="status">
      <span>{message}</span>
      {onRetry ? <Button onClick={onRetry} size="sm">Повторить</Button> : null}
    </div>
  );
}

export function ReaderLoadingState() {
  return (
    <section aria-label="Загружаем главу" aria-live="polite" className={styles.loadingState} role="status">
      <Skeleton className={styles.loadingTitle} />
      <Skeleton className={styles.loadingLine} />
      <Skeleton className={styles.loadingLine} />
      <Skeleton className={styles.loadingLineShort} />
    </section>
  );
}

export function ReaderLocatorAlert({ kind, onOpenCurrent }: { kind: ReaderSourceProblem | 'replaced'; onOpenCurrent?: () => void }) {
  const messages = {
    invalid: ['Ссылка на источник недействительна', 'Откройте источник заново из поиска или выберите актуальную главу.'],
    unavailable: ['Источник недоступен', 'Запрошенная глава больше недоступна. Выберите другой источник.'],
    paragraph: ['Фрагмент больше не найден', 'Запрошенный абзац отсутствует в текущем тексте. Другой абзац не выбран.'],
    version: ['Источник изменился', 'Глава изменилась с тех пор, как на неё сослались.'],
    replaced: ['Открыта другая глава', 'Запрошенная глава недоступна. Показана первая доступная глава.'],
  };
  return (
    <Alert className={styles.locatorAlert} tone={kind === 'replaced' ? 'info' : 'warning'}>
      {kind === 'replaced' ? <Info aria-hidden="true" size={16} /> : <TriangleAlert aria-hidden="true" size={16} />}
      <span>
        <strong>{messages[kind][0]}</strong> {messages[kind][1]}
      </span>
      {onOpenCurrent ? <Button onClick={onOpenCurrent} size="sm">Открыть актуальную</Button> : null}
    </Alert>
  );
}

export function ReaderEmptyState({ onBack }: { onBack: () => void }) {
  return (
    <section aria-label="Нет доступных глав" className={styles.workspaceState}>
      <div className={styles.card}>
        <BookOpen aria-hidden="true" size={36} />
        <h1>Нет доступных глав</h1>
        <p>Восстановите удалённую главу или загрузите книгу заново, чтобы продолжить чтение.</p>
        <Button onClick={onBack} variant="primary">К моим книгам</Button>
      </div>
    </section>
  );
}

export function ReaderForbiddenState({ message, onBack }: { message: string; onBack: () => void }) {
  return (
    <section aria-label="Нет доступа" className={styles.workspaceState}>
      <div className={styles.card}>
        <ShieldX aria-hidden="true" size={36} />
        <h1>Нет доступа к книге</h1>
        <p>{message || 'У вас нет доступа к этой саге или книге. Вернитесь на полку и выберите доступный материал.'}</p>
        <Button onClick={onBack} variant="primary">На полку</Button>
      </div>
    </section>
  );
}
