-- The prepared map names its chapters by their places, «Книга 2, гл. 12 „Белый порт“», beside their keys
-- (specs/2026-11-10-ux1-names.md N4.1). Every map prepared before this migration holds chapters without a place: it is
-- derived from the cards of the saga, so it goes, and the catch-up at start prepares it again by the new rules
-- (specs/2026-11-04-saga-map.md R10.3); until then the saga says its map is being prepared. The nodes, their parts and
-- names go with their graph; what the author took off the map (saga_map_hidden) stays.
DELETE FROM public.saga_map_graphs WHERE built_at <= now();

REVOKE ALL ON TABLE public.saga_map_graphs FROM PUBLIC;
