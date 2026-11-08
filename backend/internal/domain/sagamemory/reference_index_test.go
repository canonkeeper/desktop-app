package sagamemory

import "sort"

// buildReferenceIndex is the whole-version paragraph index as the backend kept it in memory
// before specs/2026-10-28-saga-search-in-postgres.md (BuildIndex of 179a1cc4): the reference the
// stored rows must reproduce exactly. stems maps normalized words to russian_stem lexemes; an
// empty lexeme marks a stop word, a missing word stems to itself.
func buildReferenceIndex(version *Version, stems map[string]string) *Index {
	index := &Index{
		Version: version, stems: stems,
		postings: map[string][]posting{}, surface: map[string]map[string]struct{}{},
	}
	words := map[string]struct{}{}
	total := 0
	for chapterIndex := range version.Chapters {
		chapter := &version.Chapters[chapterIndex]
		index.chapterStart = append(index.chapterStart, int32(len(index.paras)))
		for paraIndex := range chapter.Paragraphs {
			id := int32(len(index.paras))
			index.paras = append(index.paras, paraRef{chapter: chapterIndex, para: paraIndex})
			byStem := map[string][]int32{}
			tokens := Tokenize(chapter.Paragraphs[paraIndex].Norm)
			for _, token := range tokens {
				words[token.Word] = struct{}{}
				stem := index.stem(token.Word)
				if stem == "" {
					continue
				}
				byStem[stem] = append(byStem[stem], int32(token.Pos))
				if index.surface[stem] == nil {
					index.surface[stem] = map[string]struct{}{}
				}
				index.surface[stem][token.Word] = struct{}{}
			}
			for stem, positions := range byStem {
				index.postings[stem] = append(index.postings[stem], posting{para: id, positions: positions})
			}
			index.docLen = append(index.docLen, len(tokens))
			total += len(tokens)
		}
	}
	index.chapterStart = append(index.chapterStart, int32(len(index.paras)))
	for word := range words {
		index.vocab = append(index.vocab, word)
	}
	sort.Strings(index.vocab)
	if len(index.paras) > 0 {
		index.avgLen = float64(total) / float64(len(index.paras))
	}
	return index
}
