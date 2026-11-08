package deletion

import (
	"reflect"
	"testing"

	"canonkeeper/backend/internal/governance"
)

func TestSourceDeletionTargetsReturnsOrderedCopy(t *testing.T) {
	want := []governance.DeletionTarget{
		governance.TargetRuntime,
		governance.TargetFilesystem,
		governance.TargetPostgreSQL,
	}
	got := SourceDeletionTargets()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	got[0] = governance.TargetPostgreSQL
	if again := SourceDeletionTargets(); again[0] != governance.TargetRuntime {
		t.Fatalf("caller mutated target order: %v", again)
	}
}
