import { SupportReference } from '../../ui/feedback/SupportReference';
import { Confirm, deletionLines } from '../../ui/overlays/Confirm';
import type { useResearchChatDelete } from './research-chat-delete';

/**
 * «Удалить чат «…»?» (specs/2026-11-10-ux1-chat.md CH2.3, specs/2026-11-10-ux2-patterns-actions.md PA3.1): the one
 * confirmation, opened from «Действия чата». A chat does not come back; a refusal stays in the window with its code, and
 * the action tries again. It belongs to no question field, so a chat without one is deleted too.
 */
export function ChatDeleteDialog({ flow }: { flow: ReturnType<typeof useResearchChatDelete> }) {
  if (!flow.open) return null;
  const { error, errorCode, isPending: deleting, targetTitle: title } = flow;
  return (
    <Confirm
      action="Удалить чат"
      busy={deleting}
      error={error ? <>{error}{errorCode ? <> <SupportReference code={errorCode} /></> : null}</> : null}
      irreversible
      onCancel={flow.dismiss}
      onConfirm={() => void flow.confirm()}
      title={`Удалить чат «${title}»?`}
    >
      {deletionLines.gone}
    </Confirm>
  );
}
