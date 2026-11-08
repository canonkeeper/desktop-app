package projectaccess

type DeletedSourceLifecycle string

const (
	DeletedSourceRestorable    DeletedSourceLifecycle = "restorable"
	DeletedSourceExpired       DeletedSourceLifecycle = "expired"
	DeletedSourceTerminal      DeletedSourceLifecycle = "terminal"
	DeletedSourceParentDeleted DeletedSourceLifecycle = "parent_deleted"
)
