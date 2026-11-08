package researchchat

import (
	"context"
	"fmt"
	"strings"

	"canonkeeper/backend/internal/domain/authormaterials"
	"canonkeeper/backend/internal/domain/sagamemory"
)

// The author's materials in the tools of the chat and the helpers (specs/2026-11-04-saga-map.md, R9.7). A record is not a
// chapter, so it enters no manifest. It is always signed: what the book does not know is a plan, not an event of the book,
// and what the book knows yields to the text.

const (
	materialPlan = "план автора, в книге ещё нет"
	materialBook = "по материалам автора, текст книги важнее"
	// maxMaterialsShown bounds the records one search or glossary call lists.
	maxMaterialsShown = 10
	// materialExcerptRunes bounds the description of a record in a list; entity reads the whole of it.
	materialExcerptRunes = 300
)

// MaterialsReader reads the records of a project for a call whose authority is checked.
type MaterialsReader interface {
	AuthorMaterials(ctx context.Context, projectID string) ([]authormaterials.Record, error)
}

// WithMaterials lets the tools read the author's materials.
func (tools *ReadTools) WithMaterials(reader MaterialsReader) *ReadTools {
	tools.materials = reader
	return tools
}

func (tools *ReadTools) projectMaterials(ctx context.Context, projectID string) ([]authormaterials.Record, error) {
	if tools.materials == nil {
		return nil, nil
	}
	return tools.materials.AuthorMaterials(ctx, projectID)
}

var materialKindNames = map[authormaterials.Kind]string{
	authormaterials.KindSkill: "навык", authormaterials.KindPlace: "место", authormaterials.KindCharacter: "персонаж",
	authormaterials.KindItem: "предмет", authormaterials.KindFaction: "фракция", authormaterials.KindOther: "другое",
}

// materialLabel signs a record by whether the book knows its name.
func materialLabel(record authormaterials.Record, glossary *sagamemory.Glossary) string {
	if _, known := glossary.Lookup(record.Name); known {
		return materialBook
	}
	return materialPlan
}

// materialByName finds the record of a name, in any of its cases of writing.
func materialByName(records []authormaterials.Record, name string) (authormaterials.Record, bool) {
	wanted := sagamemory.GlossaryKey(name)
	for _, record := range records {
		if sagamemory.GlossaryKey(record.Name) == wanted {
			return record, true
		}
	}
	return authormaterials.Record{}, false
}

// renderMaterial is the whole of a record.
func renderMaterial(record authormaterials.Record, label string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s (%s; %s)", record.Name, materialKindNames[record.Kind], label)
	if record.Description != "" {
		out.WriteString("\n" + record.Description)
	}
	for _, property := range record.Properties {
		fmt.Fprintf(&out, "\n%s: %s", property.Key, property.Value)
	}
	if record.SourceName != "" {
		fmt.Fprintf(&out, "\nИсточник: %s", record.SourceName)
	}
	return out.String()
}

// materialMatches lists the records whose name or text holds the query.
func materialMatches(records []authormaterials.Record, query string) []authormaterials.Record {
	wanted := sagamemory.Normalize(strings.TrimSpace(query))
	var found []authormaterials.Record
	for _, record := range records {
		if strings.Contains(sagamemory.Normalize(record.Name), wanted) || strings.Contains(sagamemory.Normalize(record.Description), wanted) {
			found = append(found, record)
		}
	}
	return found
}

// writeMaterialList adds the records found to the answer of a list, with a short excerpt of each.
func writeMaterialList(out *strings.Builder, records []authormaterials.Record, glossary *sagamemory.Glossary) {
	if len(records) == 0 {
		return
	}
	fmt.Fprintf(out, "Материалы автора (не текст книги; целиком читает entity): %d\n", len(records))
	for index, record := range records {
		if index == maxMaterialsShown {
			fmt.Fprintf(out, "… и ещё %d\n", len(records)-maxMaterialsShown)
			break
		}
		fmt.Fprintf(out, "- %s (%s; %s)", record.Name, materialKindNames[record.Kind], materialLabel(record, glossary))
		if excerpt := materialExcerpt(record.Description); excerpt != "" {
			out.WriteString(" — " + excerpt)
		}
		out.WriteByte('\n')
	}
}

func materialExcerpt(text string) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) <= materialExcerptRunes {
		return string(runes)
	}
	return string(runes[:materialExcerptRunes]) + "…"
}
