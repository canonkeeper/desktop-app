-- The italic and bold of a chapter are part of its text (specs/2026-11-08-italic-bold-in-text.md, TF1, TF3.1).

-- A paragraph of the book holds its faces in markdown, in the Markdown subset: *italic*, **bold**, ***both***, with a
-- star and a backslash of the text escaped. A paragraph written before is its plain text there; its stars and
-- backslashes are escaped, so it reads as the same text («* * *» stays three stars, not italic). plain_text, the text the
-- fingerprint of a chapter, its index and its cards are made of, does not change.
UPDATE public.chapter_paragraphs
SET markdown = replace(replace(markdown, '\', '\\'), '*', '\*')
WHERE markdown = plain_text AND (strpos(markdown, '\') > 0 OR strpos(markdown, '*') > 0);
REVOKE ALL ON TABLE public.chapter_paragraphs FROM PUBLIC;

-- In a working text the signs ⟨ ⟩ ⟪ ⟫ are only the marks of italic and bold (TF1.5). Those that came before as text — from
-- a file, a paste or a model — become the brackets they look like, ‹ › « », in the working text, its versions and the
-- quotes of its notes, which find their places in it. A draft changed moves its revision, so a page still holding the
-- old text cannot write it over.
UPDATE public.chapter_drafts
SET body = translate(body, '⟨⟩⟪⟫', '‹›«»'), revision = revision + 1
WHERE body ~ '[⟨⟩⟪⟫]';
UPDATE public.chapter_draft_versions
SET text = translate(text, '⟨⟩⟪⟫', '‹›«»')
WHERE text ~ '[⟨⟩⟪⟫]';
UPDATE public.chapter_draft_notes
SET quote = translate(quote, '⟨⟩⟪⟫', '‹›«»'), replacement = translate(replacement, '⟨⟩⟪⟫', '‹›«»')
WHERE quote ~ '[⟨⟩⟪⟫]' OR replacement ~ '[⟨⟩⟪⟫]';
REVOKE ALL ON TABLE public.chapter_drafts FROM PUBLIC;
REVOKE ALL ON TABLE public.chapter_draft_versions FROM PUBLIC;
REVOKE ALL ON TABLE public.chapter_draft_notes FROM PUBLIC;
