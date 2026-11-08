-- «Как с нами связаться» and the feedback of authors and guests (specs/2026-11-07-contacts.md).

-- C1.3: the contacts the administrator sets. One row at most; it appears on the first change. NULL — and no row — is
-- a contact not given: nothing is shown for it.
CREATE TABLE public.platform_contacts (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  telegram text CHECK (telegram ~ '^https://t\.me/[A-Za-z][A-Za-z0-9_]{4,31}$'),
  email text CHECK (email = btrim(email) AND char_length(email) BETWEEN 3 AND 254 AND position('@' IN email) > 1),
  note text CHECK (note = btrim(note) AND char_length(note) BETWEEN 1 AND 120 AND position(E'\n' IN note) = 0),
  updated_at timestamptz NOT NULL,
  updated_by uuid NOT NULL REFERENCES public.users(id)
);
REVOKE ALL ON TABLE public.platform_contacts FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.platform_contacts TO canonkeeper_product_runtime;

-- F1.7, F3: a thread of feedback — the author's (user_id) or a guest's (NULL) — with the address to answer to. A
-- guest's thread is kept until expires_at, 90 days after its last message (F4.2); an author's goes with his account.
-- author_read_at is when the author last opened it: a reply of support newer than it is unread (F3.2).
CREATE TABLE public.feedback_threads (
  id uuid PRIMARY KEY,
  user_id uuid REFERENCES public.users(id) ON DELETE CASCADE,
  email text NOT NULL CHECK (email = btrim(email) AND char_length(email) BETWEEN 3 AND 254),
  topic text NOT NULL CHECK (topic IN ('bug', 'idea', 'question', 'other')),
  subject text NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 80),
  status text NOT NULL CHECK (status IN ('new', 'answered')),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  author_read_at timestamptz,
  expires_at timestamptz,
  CONSTRAINT feedback_threads_guest_expiry_check CHECK ((user_id IS NULL) = (expires_at IS NOT NULL))
);
CREATE INDEX feedback_threads_user_idx ON public.feedback_threads (user_id, updated_at DESC) WHERE user_id IS NOT NULL;
CREATE INDEX feedback_threads_inbox_idx ON public.feedback_threads ((status = 'new') DESC, updated_at DESC);
CREATE INDEX feedback_threads_expiry_idx ON public.feedback_threads (expires_at) WHERE expires_at IS NOT NULL;
REVOKE ALL ON TABLE public.feedback_threads FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.feedback_threads TO canonkeeper_product_runtime;

-- F1.2, F2: one message of a thread — the author's or support's — as blocks of text and images in their order, never
-- HTML. draft_hash is the SHA-256 of the browser's draft key: a repeat of the same draft is the same message (F1.4).
-- context is what the page sent with an author's message: the path, the browser, the width, and the build.
CREATE TABLE public.feedback_messages (
  id uuid PRIMARY KEY,
  thread_id uuid NOT NULL REFERENCES public.feedback_threads(id) ON DELETE CASCADE,
  number integer NOT NULL CHECK (number BETWEEN 1 AND 1000),
  sender text NOT NULL CHECK (sender IN ('author', 'support')),
  blocks jsonb NOT NULL CHECK (jsonb_typeof(blocks) = 'array' AND jsonb_array_length(blocks) BETWEEN 1 AND 200),
  context jsonb CHECK (context IS NULL OR jsonb_typeof(context) = 'object'),
  draft_hash text NOT NULL UNIQUE CHECK (draft_hash ~ '^[0-9a-f]{64}$'),
  created_at timestamptz NOT NULL,
  CONSTRAINT feedback_messages_number_key UNIQUE (thread_id, number)
);
REVOKE ALL ON TABLE public.feedback_messages FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON TABLE public.feedback_messages TO canonkeeper_product_runtime;

-- F1.3, F1.4: an image of a message, kept as files feedback/<id>.* under file_storage_dir. It is uploaded before the
-- message into the draft of its uploader (draft_hash; user_id when signed in) and joins the message that is sent with
-- it. One never sent is removed after a day; the files of a removed row go with the hourly cleanup (F4.2).
CREATE TABLE public.feedback_images (
  id uuid PRIMARY KEY,
  user_id uuid REFERENCES public.users(id) ON DELETE CASCADE,
  draft_hash text NOT NULL CHECK (draft_hash ~ '^[0-9a-f]{64}$'),
  message_id uuid REFERENCES public.feedback_messages(id) ON DELETE CASCADE,
  width integer NOT NULL CHECK (width > 0),
  height integer NOT NULL CHECK (height > 0),
  bytes bigint NOT NULL CHECK (bytes > 0),
  created_at timestamptz NOT NULL
);
CREATE INDEX feedback_images_draft_idx ON public.feedback_images (draft_hash) WHERE message_id IS NULL;
CREATE INDEX feedback_images_message_idx ON public.feedback_images (message_id) WHERE message_id IS NOT NULL;
CREATE INDEX feedback_images_user_idx ON public.feedback_images (user_id) WHERE user_id IS NOT NULL;
REVOKE ALL ON TABLE public.feedback_images FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.feedback_images TO canonkeeper_product_runtime;
