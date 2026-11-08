package deletion

import "errors"

type PermissionLossScope struct {
	ProjectID   string
	ActorUserID string
}

func (scope PermissionLossScope) Validate() error {
	if !canonicalUUID(scope.ProjectID) || !canonicalUUID(scope.ActorUserID) {
		return errors.New("deletion: permission loss scope has an invalid identity")
	}
	return nil
}
