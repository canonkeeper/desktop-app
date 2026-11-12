import type { CanonKeeperApiClient } from '../../shared/api';
import { createBookOperations } from './repository/books';
import { createChapterOperations } from './repository/chapters';
import { createChatOperations } from './repository/chat';
import { createMockRepositoryContext } from './repository/context';
import { createAgentTimelineOperations } from './repository/agent-timeline';
import { createJobOperations } from './repository/jobs';
import { createIndexingOperations } from './repository/indexing';
import { createImportQueueOperations } from './repository/import-queues';
import { createProjectOperations } from './repository/projects';
import { createReaderOperations } from './repository/reader';
import { createSessionOperations } from './repository/session';
import { createNotesOperations } from './repository/notes';
import { createAuthorMaterialOperations } from './repository/author-materials';
import { createAuthorMemoryOperations } from './repository/author-memory';
import { createChapterDraftOperations } from './repository/chapter-drafts';
import { createLettersOperations } from './repository/letters';
import { createVoiceOperations } from './repository/voice';
import { createSagaOperations } from './repository/saga';
import { createQuotaOperations } from './repository/quota';
import { createImageOperations } from './repository/images';
import { createImageDrawingOperations } from './repository/image-drawings';
import { createImageUploadOperations } from './repository/image-uploads';
import { createDrawingOperations } from './repository/drawings';
import { createAdminOperations } from './repository/admin';
import { createIllustrationAccessOperations } from './repository/illustration-access';
import { createPromoPermissionOperations } from './repository/promo-permissions';
import { createReadingTestOperations } from './repository/reading-test';
import { createContactOperations } from './repository/contact';
import { createTierOperations } from './repository/tiers';
import { createTierKeyOperations } from './repository/tier-keys';
import { createTrashOperations } from './repository/trash';

export function createMockApiClient() {
  const context = createMockRepositoryContext();
  const chat = createChatOperations(context);
  const jobs = createJobOperations(context);
  const materials = createAuthorMaterialOperations(context);

  return {
    ...createSessionOperations(context),
    ...createProjectOperations(context),
    ...createBookOperations(context),
    ...createImportQueueOperations(context),
    ...jobs,
    ...createIndexingOperations(context),
    ...createChapterOperations(context),
    ...createReaderOperations(context),
    ...chat,
    ...createAgentTimelineOperations(context),
    ...createNotesOperations(context),
    ...createSagaOperations(context, materials.materialsOf),
    ...materials,
    ...createAuthorMemoryOperations(context),
    ...createChapterDraftOperations(context, chat, jobs),
    ...createVoiceOperations(context),
    ...createLettersOperations(context),
    ...createQuotaOperations(context),
    ...createImageOperations(context),
    ...createImageDrawingOperations(context),
    ...createImageUploadOperations(context),
    ...createDrawingOperations(context, chat),
    ...createAdminOperations(context),
    ...createIllustrationAccessOperations(context),
    ...createPromoPermissionOperations(context),
    ...createReadingTestOperations(context),
    ...createContactOperations(context),
    ...createTierOperations(context),
    ...createTierKeyOperations(context),
    ...createTrashOperations(context),
  } satisfies CanonKeeperApiClient;
}

export type MockRepository = ReturnType<typeof createMockApiClient>;
