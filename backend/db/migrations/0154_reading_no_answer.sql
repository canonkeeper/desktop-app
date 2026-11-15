-- A provider of «Свой ИИ» that does not answer, and a chapter it does not take
-- (specs/2026-11-15-desk-reading-no-answer.md).
--
-- NA1.1, NA1.2: the reading of the books stands at «провайдер не отвечает» — a request of the reading nobody answered through
-- the last attempt of the client: a 5xx, a wait the provider asked for, a stream torn mid-answer —, and the author goes on.
ALTER TABLE public.card_build_permissions
    DROP CONSTRAINT card_build_permissions_pause_check,
    ADD CONSTRAINT card_build_permissions_pause_check CHECK (
      (state = 'allowed' AND pause IS NULL)
      OR (state = 'paused' AND pause IN (
        'author', 'sum', 'funds', 'key', 'model', 'connection', 'no_answer', 'prices', 'characters', 'reading_fence', 'unfit',
        'unavailable'
      ))
    );

-- NA2.1: a chapter whose request the provider refused for good — longer than the window of the model, a request too large —
-- is marked refused at once, by the rule of every mark.
ALTER TABLE public.reading_unread_chapters
    DROP CONSTRAINT reading_unread_chapters_reason_check,
    ADD CONSTRAINT reading_unread_chapters_reason_check CHECK (reason IN ('cut_answer', 'unreadable', 'refused'));

-- The constraints change; the privileges of both tables do not.
REVOKE ALL ON TABLE public.card_build_permissions FROM PUBLIC;
REVOKE ALL ON TABLE public.reading_unread_chapters FROM PUBLIC;
