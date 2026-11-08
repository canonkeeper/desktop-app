package deletion

import "canonkeeper/backend/internal/governance"

var sourceDeletionTargetOrder = []governance.DeletionTarget{
	governance.TargetRuntime,
	governance.TargetFilesystem,
	governance.TargetPostgreSQL,
}

func SourceDeletionTargets() []governance.DeletionTarget {
	return cloneDeletionTargets(sourceDeletionTargetOrder)
}

func cloneDeletionTargets(targets []governance.DeletionTarget) []governance.DeletionTarget {
	return append([]governance.DeletionTarget(nil), targets...)
}
