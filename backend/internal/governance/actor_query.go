package governance

type ActorQuery string

const (
	QueryListProjectsForActor                          ActorQuery = "ListProjectsForActor"
	QueryListProjectInvitationsForActor                ActorQuery = "ListProjectInvitationsForActor"
	QueryResolveBookAccessForActor                     ActorQuery = "ResolveBookAccessForActor"
	QueryResolveChapterAccessForActor                  ActorQuery = "ResolveChapterAccessForActor"
	QueryResolveProjectInvitationMemberAccessForActor  ActorQuery = "ResolveProjectInvitationMemberAccessForActor"
	QueryResolveProjectInvitationInviteeAccessForActor ActorQuery = "ResolveProjectInvitationInviteeAccessForActor"
	QueryResolveJobAccessForActor                      ActorQuery = "ResolveJobAccessForActor"
)
