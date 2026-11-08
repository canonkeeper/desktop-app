package governance

import (
	"reflect"
	"testing"
)

func TestActorQueryRegistryIsClosedAndComplete(t *testing.T) {
	collection := func(name ActorQuery, proof ActorQueryProof) ActorQueryRegistration {
		return ActorQueryRegistration{
			Name: name, Kind: ActorQueryCollection, ActorIDSource: ActorIDCurrentUser,
			Proof: proof, ResultShape: ActorQueryExistingOpenAPIFields,
			Pagination: ActorQueryBoundedPagination, RequiresProjectScopedFollowUp: true,
		}
	}
	resolver := func(name ActorQuery, proof ActorQueryProof) ActorQueryRegistration {
		return ActorQueryRegistration{
			Name: name, Kind: ActorQueryResourceResolver, ActorIDSource: ActorIDCurrentUser,
			Proof: proof, ResultShape: ActorQueryOwningIdentityTuple,
			Pagination: ActorQueryNoPagination, RequiresProjectScopedFollowUp: true,
		}
	}
	want := []ActorQueryRegistration{
		collection(QueryListProjectsForActor, ActorProofActiveMembership),
		collection(QueryListProjectInvitationsForActor, ActorProofExactInvitee),
		resolver(QueryResolveBookAccessForActor, ActorProofActiveMembership),
		resolver(QueryResolveChapterAccessForActor, ActorProofActiveMembership),
		resolver(QueryResolveProjectInvitationMemberAccessForActor, ActorProofActiveMembership),
		resolver(QueryResolveProjectInvitationInviteeAccessForActor, ActorProofExactInvitee),
		resolver(QueryResolveJobAccessForActor, ActorProofActiveMembership),
	}

	got := ActorQueryRegistry()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ActorQueryRegistry() mismatch\n got: %#v\nwant: %#v", got, want)
	}
	if err := ValidateActorQueryRegistry(got); err != nil {
		t.Fatalf("ValidateActorQueryRegistry() error = %v", err)
	}

	got[0].RequiresProjectScopedFollowUp = false
	if fresh := ActorQueryRegistry(); !reflect.DeepEqual(fresh, want) {
		t.Fatal("ActorQueryRegistry returned mutable shared state")
	}
}

func TestActorQueryRegistryRejectsAuthorityWidening(t *testing.T) {
	valid := ActorQueryRegistry()
	tests := []struct {
		name   string
		mutate func([]ActorQueryRegistration) []ActorQueryRegistration
	}{
		{name: "missing query", mutate: func(rows []ActorQueryRegistration) []ActorQueryRegistration { return rows[1:] }},
		{name: "duplicate query", mutate: func(rows []ActorQueryRegistration) []ActorQueryRegistration { return append(rows, rows[0]) }},
		{name: "unknown generic resolver", mutate: func(rows []ActorQueryRegistration) []ActorQueryRegistration {
			rows[2].Name = ActorQuery("ResolveResourceAccessForActor")
			return rows
		}},
		{name: "request supplied actor", mutate: func(rows []ActorQueryRegistration) []ActorQueryRegistration {
			rows[0].ActorIDSource = ActorIDSource("request")
			return rows
		}},
		{name: "missing actor predicate", mutate: func(rows []ActorQueryRegistration) []ActorQueryRegistration { rows[0].ActorIDSource = ""; return rows }},
		{name: "resolver product body", mutate: func(rows []ActorQueryRegistration) []ActorQueryRegistration {
			rows[2].ResultShape = ActorQueryExistingOpenAPIFields
			return rows
		}},
		{name: "unbounded collection", mutate: func(rows []ActorQueryRegistration) []ActorQueryRegistration {
			rows[0].Pagination = ActorQueryNoPagination
			return rows
		}},
		{name: "resolver pagination", mutate: func(rows []ActorQueryRegistration) []ActorQueryRegistration {
			rows[2].Pagination = ActorQueryBoundedPagination
			return rows
		}},
		{name: "wrong invitee proof", mutate: func(rows []ActorQueryRegistration) []ActorQueryRegistration {
			rows[5].Proof = ActorProofActiveMembership
			return rows
		}},
		{name: "drops downstream project scope", mutate: func(rows []ActorQueryRegistration) []ActorQueryRegistration {
			rows[2].RequiresProjectScopedFollowUp = false
			return rows
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := append([]ActorQueryRegistration(nil), valid...)
			if err := ValidateActorQueryRegistry(tt.mutate(rows)); err == nil {
				t.Fatal("ValidateActorQueryRegistry() error = nil")
			}
		})
	}
}
