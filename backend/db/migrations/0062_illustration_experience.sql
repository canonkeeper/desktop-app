-- Saga illustrations, the author experience (specs/2026-10-29-saga-illustrations.md UC8, R9.4).

-- R8.1: the drawing develops in the feed. draw_phase is where the Job of a drawing still drawn
-- is (queued — no worker yet, reading — gathering the images it is drawn from, drawing — the model
-- draws); partial_frame is the last draft frame kept as <id>.partial.jpg, 0 — none yet.
-- R8.2: sources are the source links (key/from-to) of the paragraphs the description stands on.
-- R8.6: sketch marks a style sketch of the first drawing. R8.7: kind sheet is a character sheet.
-- R8.15: render_revision counts the renders of the drawing's file from its base and its layers.
ALTER TABLE public.saga_images
  DROP CONSTRAINT saga_images_kind_check,
  ADD CONSTRAINT saga_images_kind_check CHECK (kind IN ('character', 'place', 'scene', 'item', 'other', 'sheet')),
  ADD COLUMN draw_phase text NOT NULL DEFAULT 'queued' CHECK (draw_phase IN ('queued', 'reading', 'drawing')),
  ADD COLUMN partial_frame integer NOT NULL DEFAULT 0 CHECK (partial_frame BETWEEN 0 AND 3),
  ADD COLUMN sources text[] NOT NULL DEFAULT '{}' CHECK (cardinality(sources) <= 10),
  ADD COLUMN sketch boolean NOT NULL DEFAULT false,
  ADD COLUMN render_revision integer NOT NULL DEFAULT 0 CHECK (render_revision >= 0);
REVOKE ALL ON TABLE public.saga_images FROM PUBLIC;

-- R8.4, R8.15: a region edit is a layer of a drawing, drawn by its own illustration Job: the
-- selection in the drawing's pixels, the instruction and whether a brush shape narrows it. The
-- patch with its feathered alpha is <id>.layer.png, the alpha <id>.alpha.png, the brush shape
-- <id>.brush.png. The drawing's file is its base with the visible ready layers in position order.
CREATE TABLE public.saga_image_layers (
  id uuid PRIMARY KEY,
  project_id uuid NOT NULL REFERENCES public.projects(id) ON DELETE CASCADE,
  image_id uuid NOT NULL,
  position integer NOT NULL CHECK (position >= 1),
  status text NOT NULL CHECK (status IN ('drawing', 'ready', 'failed', 'refused')),
  draw_phase text NOT NULL DEFAULT 'queued' CHECK (draw_phase IN ('queued', 'reading', 'drawing')),
  partial_frame integer NOT NULL DEFAULT 0 CHECK (partial_frame BETWEEN 0 AND 3),
  instruction text NOT NULL CHECK (char_length(btrim(instruction)) BETWEEN 1 AND 4000),
  rect_x integer NOT NULL CHECK (rect_x >= 0),
  rect_y integer NOT NULL CHECK (rect_y >= 0),
  rect_width integer NOT NULL CHECK (rect_width > 0),
  rect_height integer NOT NULL CHECK (rect_height > 0),
  brush boolean NOT NULL DEFAULT false,
  visible boolean NOT NULL DEFAULT true,
  model text NOT NULL CHECK (model = btrim(model) AND char_length(model) BETWEEN 1 AND 120),
  job_id uuid NOT NULL,
  chat_id uuid,
  turn_id uuid,
  created_by uuid REFERENCES public.users(id),
  error_code text CHECK (error_code IS NULL OR error_code IN ('draw_refused', 'draw_failed')),
  error_message text CHECK (error_message IS NULL OR char_length(error_message) <= 500),
  created_at timestamptz NOT NULL,
  ready_at timestamptz,
  CONSTRAINT saga_image_layers_position_key UNIQUE (project_id, image_id, position),
  CONSTRAINT saga_image_layers_ready_check CHECK ((status = 'ready') = (ready_at IS NOT NULL)),
  CONSTRAINT saga_image_layers_error_check CHECK (
    (status = 'refused' AND error_code = 'draw_refused')
    OR (status = 'failed' AND error_code = 'draw_failed')
    OR (status IN ('drawing', 'ready') AND error_code IS NULL AND error_message IS NULL)
  ),
  CONSTRAINT saga_image_layers_image_fkey FOREIGN KEY (project_id, image_id)
    REFERENCES public.saga_images(project_id, id) ON DELETE CASCADE
);

REVOKE ALL ON TABLE public.saga_image_layers FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.saga_image_layers TO canonkeeper_product_runtime;
GRANT SELECT ON TABLE public.saga_image_layers TO canonkeeper_governance_owner;

-- R9.4: a character sheet is the preferred reference, so it keeps its own place beside the other
-- reference of the same entry and chapter.
ALTER TABLE public.saga_image_references
  ADD COLUMN sheet boolean NOT NULL DEFAULT false,
  DROP CONSTRAINT saga_image_references_pkey,
  ADD CONSTRAINT saga_image_references_pkey PRIMARY KEY (project_id, subject_key, from_chapter, sheet);
REVOKE ALL ON TABLE public.saga_image_references FROM PUBLIC;

-- R8.10: the «Иллюстрации» chat the drawing buttons of the project post into. A plain id, as the
-- chat of a drawing: deleting the chat leaves it stale and the next request opens a new chat.
ALTER TABLE public.project_illustration_settings
  ADD COLUMN chat_id uuid;
REVOKE ALL ON TABLE public.project_illustration_settings FROM PUBLIC;
