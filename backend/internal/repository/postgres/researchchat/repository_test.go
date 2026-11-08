package researchchat

import (
	"errors"
	"testing"

	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	canonstore "canonkeeper/backend/internal/store/canon"
)

func TestMapErrorReturnsTypedSubmitErrorsForCanonicalSourceState(t *testing.T) {
	if err := mapError(canonstore.ErrCanonicalSourceValidation); !errors.Is(err, researchdomain.ErrValidation) {
		t.Fatalf("canonical validation error = %v", err)
	}
	if err := mapError(canonstore.ErrCanonicalSourceNotCurrent); !errors.Is(err, researchdomain.ErrConflict) {
		t.Fatalf("stale canonical source error = %v", err)
	}
}

func TestToolReadsTreatSourceThatIsNoLongerCurrentAsStale(t *testing.T) {
	if err := mapSourceReadError(canonstore.ErrCanonicalSourceNotCurrent); !errors.Is(err, researchdomain.ErrStaleSource) {
		t.Fatalf("tool read of a changed source = %v, want stale source", err)
	}
}
