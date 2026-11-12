-- «Тариф и ключ» — a topic of «Написать нам» of its own (specs/2026-11-10-ux2-contact.md CT1.4): the refusals of a key,
-- the page «Тариф» and the refusals by a tier open the window with it. The check only widens: every kept thread stays as
-- it is. The code of an error the window was opened from goes in the context of a message, a jsonb object already.
ALTER TABLE public.feedback_threads DROP CONSTRAINT feedback_threads_topic_check;
ALTER TABLE public.feedback_threads ADD CONSTRAINT feedback_threads_topic_check
  CHECK (topic IN ('bug', 'idea', 'question', 'tariff', 'other'));
REVOKE ALL ON TABLE public.feedback_threads FROM PUBLIC;
