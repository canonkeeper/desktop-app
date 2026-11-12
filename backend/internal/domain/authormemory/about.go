package authormemory

import (
	"slices"

	"canonkeeper/backend/internal/domain/sagamemory"
)

// AboutKinds are the kinds of the records a saga tool brings with its answer about what they name
// (specs/2026-11-11-chat-corrections.md CR1.1): what the author decided about the facts and what he planned. His rules of
// style and what he rejected are no facts about anyone.
var AboutKinds = []Kind{KindCorrection, KindPlan}

// About keeps, in their order, the corrections and the plans among the records that are about what an answer names: the
// name of a record or its text names the subject (CR1.2).
func About(records []Record, subject sagamemory.Subject) []Record {
	var about []Record
	for _, record := range records {
		if slices.Contains(AboutKinds, record.Kind) && subject.About(record.Name, record.Text) {
			about = append(about, record)
		}
	}
	return about
}
