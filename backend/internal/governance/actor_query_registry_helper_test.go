package governance

import "errors"

type ActorQueryKind string

const (
	ActorQueryCollection       ActorQueryKind = "actor_owned_collection"
	ActorQueryResourceResolver ActorQueryKind = "resource_access_resolver"
)

type ActorIDSource string

const ActorIDCurrentUser ActorIDSource = "authenticated_current_user"

type ActorQueryProof string

const (
	ActorProofActiveMembership ActorQueryProof = "active_membership"
	ActorProofExactInvitee     ActorQueryProof = "exact_invitee_email"
)

type ActorQueryResultShape string

const (
	ActorQueryExistingOpenAPIFields ActorQueryResultShape = "existing_openapi_collection_fields"
	ActorQueryOwningIdentityTuple   ActorQueryResultShape = "owning_identity_tuple"
)

type ActorQueryPagination string

const (
	ActorQueryNoPagination      ActorQueryPagination = "none"
	ActorQueryBoundedPagination ActorQueryPagination = "bounded"
)

type ActorQueryRegistration struct {
	Name                          ActorQuery
	Kind                          ActorQueryKind
	ActorIDSource                 ActorIDSource
	Proof                         ActorQueryProof
	ResultShape                   ActorQueryResultShape
	Pagination                    ActorQueryPagination
	RequiresProjectScopedFollowUp bool
}

var actorQueryRegistry = []ActorQueryRegistration{
	actorCollection(QueryListProjectsForActor, ActorProofActiveMembership),
	actorCollection(QueryListProjectInvitationsForActor, ActorProofExactInvitee),
	actorResolver(QueryResolveBookAccessForActor, ActorProofActiveMembership),
	actorResolver(QueryResolveChapterAccessForActor, ActorProofActiveMembership),
	actorResolver(QueryResolveProjectInvitationMemberAccessForActor, ActorProofActiveMembership),
	actorResolver(QueryResolveProjectInvitationInviteeAccessForActor, ActorProofExactInvitee),
	actorResolver(QueryResolveJobAccessForActor, ActorProofActiveMembership),
}

func actorCollection(name ActorQuery, proof ActorQueryProof) ActorQueryRegistration {
	return ActorQueryRegistration{
		Name:                          name,
		Kind:                          ActorQueryCollection,
		ActorIDSource:                 ActorIDCurrentUser,
		Proof:                         proof,
		ResultShape:                   ActorQueryExistingOpenAPIFields,
		Pagination:                    ActorQueryBoundedPagination,
		RequiresProjectScopedFollowUp: true,
	}
}

func actorResolver(name ActorQuery, proof ActorQueryProof) ActorQueryRegistration {
	return ActorQueryRegistration{
		Name:                          name,
		Kind:                          ActorQueryResourceResolver,
		ActorIDSource:                 ActorIDCurrentUser,
		Proof:                         proof,
		ResultShape:                   ActorQueryOwningIdentityTuple,
		Pagination:                    ActorQueryNoPagination,
		RequiresProjectScopedFollowUp: true,
	}
}

func ActorQueryRegistry() []ActorQueryRegistration {
	return append([]ActorQueryRegistration(nil), actorQueryRegistry...)
}

func ValidateActorQueryRegistry(registry []ActorQueryRegistration) error {
	if len(registry) != len(actorQueryRegistry) {
		return errors.New("governance: actor query registry is incomplete")
	}
	want := make(map[ActorQuery]ActorQueryRegistration, len(actorQueryRegistry))
	for _, row := range actorQueryRegistry {
		want[row.Name] = row
	}
	seen := make(map[ActorQuery]struct{}, len(registry))
	for _, row := range registry {
		if _, duplicate := seen[row.Name]; duplicate {
			return errors.New("governance: duplicate actor query")
		}
		seen[row.Name] = struct{}{}
		expected, known := want[row.Name]
		if !known {
			return errors.New("governance: unknown actor query")
		}
		if row != expected {
			return errors.New("governance: actor query policy mismatch")
		}
	}
	return nil
}
