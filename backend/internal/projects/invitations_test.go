package projects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCreateProjectInvitationOwnerCanCreate(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, now)
	service := NewService(store, StaticIDsForTest("project-unused", "membership-unused"), StaticClockForTest(now))

	invitation, err := service.CreateProjectInvitation(ctx, "user-owner", "project-1", CreateProjectInvitationInput{
		Email: "  Invitee@Example.TEST  ",
		Role:  RoleEditor,
	})
	if err != nil {
		t.Fatalf("CreateProjectInvitation() error = %v", err)
	}

	if invitation.ID != "invitation-1" {
		t.Fatalf("Invitation.ID = %q, want invitation-1", invitation.ID)
	}
	if invitation.ProjectID != "project-1" || invitation.ProjectTitle != "The White Port" {
		t.Fatalf("invitation project = %q/%q, want project-1/The White Port", invitation.ProjectID, invitation.ProjectTitle)
	}
	if invitation.Email != "invitee@example.test" {
		t.Fatalf("Invitation.Email = %q, want normalized invitee@example.test", invitation.Email)
	}
	if invitation.EmailHash == "" || invitation.EmailHash == invitation.Email {
		t.Fatal("Invitation.EmailHash must be a deterministic non-raw hash")
	}
	if invitation.Role != RoleEditor || invitation.Status != ProjectInvitationStatusPending {
		t.Fatalf("Invitation role/status = %s/%s, want editor/pending", invitation.Role, invitation.Status)
	}
	if !invitation.ExpiresAt.Equal(now.Add(14 * 24 * time.Hour)) {
		t.Fatalf("Invitation.ExpiresAt = %s, want %s", invitation.ExpiresAt, now.Add(14*24*time.Hour))
	}
	if store.CreateProjectInvitationCallCountForTest() != 1 {
		t.Fatalf("CreateProjectInvitation store calls = %d, want 1", store.CreateProjectInvitationCallCountForTest())
	}
	if store.LastProjectInvitationEmailHashForTest(t) != invitation.EmailHash {
		t.Fatal("CreateProjectInvitation did not store the invitation email hash")
	}
}

func TestInvitationAccessResolversReturnOnlyOwningProof(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
	store.AddUserEmailForTest(t, "user-invitee", "invitee@example.test")
	invitation := store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
		ID:            "invitation-1",
		ProjectID:     "project-1",
		ProjectTitle:  "must-not-be-resolved",
		InviterUserID: "must-not-be-resolved",
		Email:         "invitee@example.test",
		EmailHash:     emailHashForInvitationTest("invitee@example.test"),
		Role:          RoleViewer,
		Status:        ProjectInvitationStatusPending,
		ExpiresAt:     createdAt.Add(time.Hour),
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	})

	member, err := store.ResolveProjectInvitationMemberAccessForActor(t.Context(), ResolveProjectInvitationMemberAccessStoreInput{
		ActorUserID:  "user-owner",
		InvitationID: invitation.ID,
	})
	if err != nil {
		t.Fatalf("ResolveProjectInvitationMemberAccessForActor() error = %v", err)
	}
	if member != (ProjectInvitationMemberAccess{ProjectID: "project-1", InvitationID: "invitation-1", ProjectRole: RoleOwner}) {
		t.Fatalf("member access = %#v", member)
	}
	if _, err := store.ResolveProjectInvitationMemberAccessForActor(t.Context(), ResolveProjectInvitationMemberAccessStoreInput{
		ActorUserID: "user-foreign", InvitationID: invitation.ID,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign member resolver error = %v, want ErrNotFound", err)
	}

	invitee, err := store.ResolveProjectInvitationInviteeAccessForActor(t.Context(), ResolveProjectInvitationInviteeAccessStoreInput{
		ActorUserID: "user-invitee", InvitationID: invitation.ID, EmailHash: invitation.EmailHash,
	})
	if err != nil {
		t.Fatalf("ResolveProjectInvitationInviteeAccessForActor() error = %v", err)
	}
	if invitee != (ProjectInvitationInviteeAccess{ProjectID: "project-1", InvitationID: "invitation-1"}) {
		t.Fatalf("invitee access = %#v", invitee)
	}

	memberType := reflect.TypeOf(member)
	if memberType.NumField() != 3 {
		t.Fatalf("member resolver field count = %d, want 3", memberType.NumField())
	}
	inviteeType := reflect.TypeOf(invitee)
	if inviteeType.NumField() != 2 {
		t.Fatalf("invitee resolver field count = %d, want 2", inviteeType.NumField())
	}
}

func TestInvitationMutationsCarryResolvedProjectID(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	createdAt := now.Add(-time.Hour)

	t.Run("cancel member arm", func(t *testing.T) {
		store := NewMemoryStoreForTest(t)
		store.AddProjectForTest(t, "project-1", "The White Port", "", createdAt)
		store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
		store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
			ID: "invitation-cancel", ProjectID: "project-1", Role: RoleViewer,
			Status: ProjectInvitationStatusPending, ExpiresAt: now.Add(time.Hour), CreatedAt: createdAt, UpdatedAt: createdAt,
		})
		service := NewService(store, StaticIDsForTest("unused", "unused"), StaticClockForTest(now))

		if _, err := service.CancelProjectInvitation(t.Context(), "user-owner", "invitation-cancel"); err != nil {
			t.Fatalf("CancelProjectInvitation() error = %v", err)
		}
		if store.lastCancelInvitationInput.ProjectID != "project-1" {
			t.Fatalf("cancel mutation project ID = %q, want project-1", store.lastCancelInvitationInput.ProjectID)
		}
	})

	t.Run("accept invitee arm", func(t *testing.T) {
		store := NewMemoryStoreForTest(t)
		store.AddProjectForTest(t, "project-1", "The White Port", "", createdAt)
		store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
		store.AddUserEmailForTest(t, "user-invitee", "invitee@example.test")
		store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
			ID: "invitation-accept", ProjectID: "project-1", Email: "invitee@example.test",
			EmailHash: emailHashForInvitationTest("invitee@example.test"), Role: RoleViewer,
			Status: ProjectInvitationStatusPending, ExpiresAt: now.Add(time.Hour), CreatedAt: createdAt, UpdatedAt: createdAt,
		})
		service := NewService(store, StaticIDsForTest("unused", "membership-new"), StaticClockForTest(now))

		if _, err := service.AcceptProjectInvitation(t.Context(), AuthenticatedUser{ID: "user-invitee", Email: "invitee@example.test", EmailVerified: true}, "invitation-accept"); err != nil {
			t.Fatalf("AcceptProjectInvitation() error = %v", err)
		}
		if store.lastAcceptInvitationInput.ProjectID != "project-1" {
			t.Fatalf("accept mutation project ID = %q, want project-1", store.lastAcceptInvitationInput.ProjectID)
		}
	})
}

func TestCreateProjectInvitationAdminCanCreate(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
	store.AddMembershipForTest(t, "membership-admin", "project-1", "user-admin", RoleAdmin, now)
	service := NewService(store, StaticIDsForTest("project-unused", "membership-unused"), StaticClockForTest(now))

	invitation, err := service.CreateProjectInvitation(ctx, "user-admin", "project-1", CreateProjectInvitationInput{
		Email: "invitee@example.test",
		Role:  RoleViewer,
	})
	if err != nil {
		t.Fatalf("CreateProjectInvitation() error = %v", err)
	}

	if invitation.Role != RoleViewer || invitation.Status != ProjectInvitationStatusPending {
		t.Fatalf("Invitation role/status = %s/%s, want viewer/pending", invitation.Role, invitation.Status)
	}
	if store.CreateProjectInvitationCallCountForTest() != 1 {
		t.Fatalf("CreateProjectInvitation store calls = %d, want 1", store.CreateProjectInvitationCallCountForTest())
	}
}

func TestCreateProjectInvitationEditorAndViewerReturnForbidden(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	for _, role := range []Role{RoleEditor, RoleViewer} {
		t.Run(string(role), func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
			store.AddMembershipForTest(t, "membership-actor", "project-1", "user-actor", role, now)
			service := NewService(store, StaticIDsForTest("project-unused", "membership-unused"), StaticClockForTest(now))

			_, err := service.CreateProjectInvitation(ctx, "user-actor", "project-1", CreateProjectInvitationInput{
				Email: "invitee@example.test",
				Role:  RoleViewer,
			})

			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("CreateProjectInvitation() error = %v, want ErrForbidden", err)
			}
			if store.CreateProjectInvitationCallCountForTest() != 0 {
				t.Fatal("CreateProjectInvitation() persisted a forbidden invitation")
			}
		})
	}
}

func TestCreateProjectInvitationNonMemberReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, now)
	service := NewService(store, StaticIDsForTest("project-unused", "membership-unused"), StaticClockForTest(now))

	_, err := service.CreateProjectInvitation(ctx, "user-nonmember", "project-1", CreateProjectInvitationInput{
		Email: "invitee@example.test",
		Role:  RoleViewer,
	})

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateProjectInvitation() error = %v, want ErrNotFound", err)
	}
	if store.CreateProjectInvitationCallCountForTest() != 0 {
		t.Fatal("CreateProjectInvitation() persisted a non-member invitation")
	}
}

func TestCreateProjectInvitationRejectsOwnerRole(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, now)
	service := NewService(store, StaticIDsForTest("project-unused", "membership-unused"), StaticClockForTest(now))

	_, err := service.CreateProjectInvitation(ctx, "user-owner", "project-1", CreateProjectInvitationInput{
		Email: "invitee@example.test",
		Role:  RoleOwner,
	})

	if !errors.Is(err, ErrValidation) {
		t.Fatalf("CreateProjectInvitation() error = %v, want ErrValidation", err)
	}
	if store.CreateProjectInvitationCallCountForTest() != 0 {
		t.Fatal("CreateProjectInvitation() persisted an invalid owner invitation")
	}
}

func TestCreateProjectInvitationDuplicatePendingReturnsConflict(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, now)
	service := NewService(store, StaticIDsForTest("project-unused", "membership-unused"), StaticClockForTest(now))
	if _, err := service.CreateProjectInvitation(ctx, "user-owner", "project-1", CreateProjectInvitationInput{
		Email: "invitee@example.test",
		Role:  RoleViewer,
	}); err != nil {
		t.Fatalf("CreateProjectInvitation() first error = %v", err)
	}

	_, err := service.CreateProjectInvitation(ctx, "user-owner", "project-1", CreateProjectInvitationInput{
		Email: "Invitee@Example.TEST",
		Role:  RoleEditor,
	})

	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateProjectInvitation() duplicate error = %v, want ErrConflict", err)
	}
}

func TestCreateProjectInvitationExistingActiveMemberReturnsConflict(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, now)
	store.AddUserEmailForTest(t, "user-member", "member@example.test")
	store.AddMembershipForTest(t, "membership-member", "project-1", "user-member", RoleViewer, now)
	service := NewService(store, StaticIDsForTest("project-unused", "membership-unused"), StaticClockForTest(now))

	_, err := service.CreateProjectInvitation(ctx, "user-owner", "project-1", CreateProjectInvitationInput{
		Email: "member@example.test",
		Role:  RoleEditor,
	})

	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateProjectInvitation() existing member error = %v, want ErrConflict", err)
	}
}

func TestCreateProjectInvitationRemovedPriorMemberCanBeInvited(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", now)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, now)
	store.AddUserEmailForTest(t, "user-member", "removed@example.test")
	store.AddMembershipForTest(t, "membership-member", "project-1", "user-member", RoleViewer, now)
	store.RemoveMembershipForTest(t, "project-1", "user-member")
	service := NewService(store, StaticIDsForTest("project-unused", "membership-unused"), StaticClockForTest(now))

	invitation, err := service.CreateProjectInvitation(ctx, "user-owner", "project-1", CreateProjectInvitationInput{
		Email: "removed@example.test",
		Role:  RoleViewer,
	})
	if err != nil {
		t.Fatalf("CreateProjectInvitation() removed member error = %v", err)
	}

	if invitation.Email != "removed@example.test" || invitation.Status != ProjectInvitationStatusPending {
		t.Fatalf("invitation status = %s, want pending", invitation.Status)
	}
}

func TestAcceptProjectInvitationMatchingEmailCreatesMembership(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	acceptedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
	store.AddUserEmailForTest(t, "user-invitee", "invitee@example.test")
	service := NewService(store, StaticIDsForTest("project-unused", "membership-accepted"), StaticClockForTest(acceptedAt))
	invitation := store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
		ID:            "invitation-1",
		ProjectID:     "project-1",
		ProjectTitle:  "The White Port",
		InviterUserID: "user-owner",
		Email:         "invitee@example.test",
		EmailHash:     emailHashForInvitationTest("invitee@example.test"),
		Role:          RoleEditor,
		Status:        ProjectInvitationStatusPending,
		ExpiresAt:     acceptedAt.Add(time.Hour),
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	})

	membership, err := service.AcceptProjectInvitation(ctx, AuthenticatedUser{
		ID:            "user-invitee",
		Email:         "Invitee@Example.TEST",
		EmailVerified: true,
	}, invitation.ID)
	if err != nil {
		t.Fatalf("AcceptProjectInvitation() error = %v", err)
	}

	if membership.ID != "membership-accepted" || membership.ProjectID != "project-1" || membership.UserID != "user-invitee" {
		t.Fatalf("membership identity = %#v, want accepted membership for invitee", membership)
	}
	if membership.Role != RoleEditor || membership.Status != MembershipStatusActive {
		t.Fatalf("membership role/status = %s/%s, want editor/active", membership.Role, membership.Status)
	}
	storedInvitation := store.ProjectInvitationForTest(t, invitation.ID)
	if storedInvitation.Status != ProjectInvitationStatusAccepted || storedInvitation.AcceptedAt == nil || !storedInvitation.AcceptedAt.Equal(acceptedAt) {
		t.Fatalf("stored invitation status/acceptedAt = %s/%v, want accepted at %s", storedInvitation.Status, storedInvitation.AcceptedAt, acceptedAt)
	}
}

func TestAcceptProjectInvitationRemovedMemberReactivatesMembership(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	acceptedAt := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
	store.AddMembershipForTest(t, "membership-removed", "project-1", "user-invitee", RoleViewer, createdAt)
	store.AddUserEmailForTest(t, "user-invitee", "removed@example.test")
	store.RemoveMembershipForTest(t, "project-1", "user-invitee")
	service := NewService(store, StaticIDsForTest("project-unused", "membership-new"), StaticClockForTest(acceptedAt))
	invitation := store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
		ID:            "invitation-1",
		ProjectID:     "project-1",
		ProjectTitle:  "The White Port",
		InviterUserID: "user-owner",
		Email:         "removed@example.test",
		EmailHash:     emailHashForInvitationTest("removed@example.test"),
		Role:          RoleAdmin,
		Status:        ProjectInvitationStatusPending,
		ExpiresAt:     acceptedAt.Add(time.Hour),
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	})

	membership, err := service.AcceptProjectInvitation(ctx, AuthenticatedUser{
		ID:            "user-invitee",
		Email:         "Removed@Example.TEST",
		EmailVerified: true,
	}, invitation.ID)
	if err != nil {
		t.Fatalf("AcceptProjectInvitation() removed member error = %v", err)
	}

	if membership.ID != "membership-removed" || membership.ProjectID != "project-1" || membership.UserID != "user-invitee" {
		t.Fatalf("membership identity = %#v, want reactivated existing membership", membership)
	}
	if membership.Role != RoleAdmin || membership.Status != MembershipStatusActive {
		t.Fatalf("membership role/status = %s/%s, want admin/active", membership.Role, membership.Status)
	}
	if !membership.CreatedAt.Equal(createdAt) || !membership.UpdatedAt.Equal(acceptedAt) {
		t.Fatalf("membership timestamps = %s/%s, want %s/%s", membership.CreatedAt, membership.UpdatedAt, createdAt, acceptedAt)
	}
	storedMembership := store.ActiveMembershipForTest(t, "project-1", "user-invitee")
	if storedMembership.ID != "membership-removed" || storedMembership.Role != RoleAdmin || storedMembership.Status != MembershipStatusActive {
		t.Fatalf("stored membership = %#v, want reactivated admin membership", storedMembership)
	}
	storedInvitation := store.ProjectInvitationForTest(t, invitation.ID)
	if storedInvitation.Status != ProjectInvitationStatusAccepted || storedInvitation.AcceptedAt == nil || !storedInvitation.AcceptedAt.Equal(acceptedAt) {
		t.Fatalf("stored invitation status/acceptedAt = %s/%v, want accepted at %s", storedInvitation.Status, storedInvitation.AcceptedAt, acceptedAt)
	}
}

func TestAcceptProjectInvitationRejectsActorWithDifferentStoredEmail(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
	store.AddUserEmailForTest(t, "user-wrong", "wrong@example.test")
	service := NewService(store, StaticIDsForTest("project-unused", "membership-accepted"), StaticClockForTest(now))
	invitation := store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
		ID:            "invitation-1",
		ProjectID:     "project-1",
		ProjectTitle:  "The White Port",
		InviterUserID: "user-owner",
		Email:         "invitee@example.test",
		EmailHash:     emailHashForInvitationTest("invitee@example.test"),
		Role:          RoleViewer,
		Status:        ProjectInvitationStatusPending,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	})

	_, err := service.AcceptProjectInvitation(ctx, AuthenticatedUser{
		ID:            "user-wrong",
		Email:         "invitee@example.test",
		EmailVerified: true,
	}, invitation.ID)

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("AcceptProjectInvitation() error = %v, want ErrNotFound", err)
	}
	if _, ok := store.MembershipForTest("project-1", "user-wrong"); ok {
		t.Fatal("AcceptProjectInvitation() created membership for actor whose stored email does not match invitation")
	}
	storedInvitation := store.ProjectInvitationForTest(t, invitation.ID)
	if storedInvitation.Status != ProjectInvitationStatusPending || storedInvitation.AcceptedAt != nil {
		t.Fatalf("stored invitation status/acceptedAt = %s/%v, want pending/<nil>", storedInvitation.Status, storedInvitation.AcceptedAt)
	}
}

func TestAcceptProjectInvitationRejectsUnknownStoredUser(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
	service := NewService(store, StaticIDsForTest("project-unused", "membership-accepted"), StaticClockForTest(now))
	invitation := store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
		ID:            "invitation-1",
		ProjectID:     "project-1",
		ProjectTitle:  "The White Port",
		InviterUserID: "user-owner",
		Email:         "invitee@example.test",
		EmailHash:     emailHashForInvitationTest("invitee@example.test"),
		Role:          RoleViewer,
		Status:        ProjectInvitationStatusPending,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	})

	_, err := service.AcceptProjectInvitation(ctx, AuthenticatedUser{
		ID:            "user-invitee",
		Email:         "invitee@example.test",
		EmailVerified: true,
	}, invitation.ID)

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("AcceptProjectInvitation() error = %v, want ErrNotFound", err)
	}
	if _, ok := store.MembershipForTest("project-1", "user-invitee"); ok {
		t.Fatal("AcceptProjectInvitation() created membership for unknown stored user")
	}
	storedInvitation := store.ProjectInvitationForTest(t, invitation.ID)
	if storedInvitation.Status != ProjectInvitationStatusPending || storedInvitation.AcceptedAt != nil {
		t.Fatalf("stored invitation status/acceptedAt = %s/%v, want pending/<nil>", storedInvitation.Status, storedInvitation.AcceptedAt)
	}
}

func TestAcceptProjectInvitationWrongEmailReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
	service := NewService(store, StaticIDsForTest("project-unused", "membership-accepted"), StaticClockForTest(now))
	invitation := store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
		ID:            "invitation-1",
		ProjectID:     "project-1",
		ProjectTitle:  "The White Port",
		InviterUserID: "user-owner",
		Email:         "invitee@example.test",
		EmailHash:     emailHashForInvitationTest("invitee@example.test"),
		Role:          RoleViewer,
		Status:        ProjectInvitationStatusPending,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	})

	_, err := service.AcceptProjectInvitation(ctx, AuthenticatedUser{
		ID:            "user-wrong",
		Email:         "wrong@example.test",
		EmailVerified: true,
	}, invitation.ID)

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("AcceptProjectInvitation() error = %v, want ErrNotFound", err)
	}
	if _, ok := store.MembershipForTest("project-1", "user-wrong"); ok {
		t.Fatal("AcceptProjectInvitation() created membership for wrong email")
	}
	storedInvitation := store.ProjectInvitationForTest(t, invitation.ID)
	if storedInvitation.Status != ProjectInvitationStatusPending || storedInvitation.AcceptedAt != nil {
		t.Fatalf("stored invitation status/acceptedAt = %s/%v, want pending/<nil>", storedInvitation.Status, storedInvitation.AcceptedAt)
	}
}

func TestAcceptProjectInvitationUnverifiedEmailReturnsForbidden(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
	service := NewService(store, StaticIDsForTest("project-unused", "membership-accepted"), StaticClockForTest(now))
	invitation := store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
		ID:            "invitation-1",
		ProjectID:     "project-1",
		ProjectTitle:  "The White Port",
		InviterUserID: "user-owner",
		Email:         "invitee@example.test",
		EmailHash:     emailHashForInvitationTest("invitee@example.test"),
		Role:          RoleViewer,
		Status:        ProjectInvitationStatusPending,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	})

	_, err := service.AcceptProjectInvitation(ctx, AuthenticatedUser{
		ID:            "user-invitee",
		Email:         "invitee@example.test",
		EmailVerified: false,
	}, invitation.ID)

	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("AcceptProjectInvitation() error = %v, want ErrForbidden", err)
	}
	if _, ok := store.MembershipForTest("project-1", "user-invitee"); ok {
		t.Fatal("AcceptProjectInvitation() created membership for unverified email")
	}
	storedInvitation := store.ProjectInvitationForTest(t, invitation.ID)
	if storedInvitation.Status != ProjectInvitationStatusPending || storedInvitation.AcceptedAt != nil {
		t.Fatalf("stored invitation status/acceptedAt = %s/%v, want pending/<nil>", storedInvitation.Status, storedInvitation.AcceptedAt)
	}
}

func TestActorCollectionListMyProjectInvitationsRequiresVerifiedEmail(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
	store.AddUserEmailForTest(t, "user-invitee", "invitee@example.test")
	store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
		ID:            "invitation-1",
		ProjectID:     "project-1",
		ProjectTitle:  "The White Port",
		InviterUserID: "user-owner",
		Email:         "invitee@example.test",
		EmailHash:     emailHashForInvitationTest("invitee@example.test"),
		Role:          RoleViewer,
		Status:        ProjectInvitationStatusPending,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	})
	service := NewService(store, StaticIDsForTest("project-unused", "membership-unused"), StaticClockForTest(now))

	_, err := service.ListMyProjectInvitations(ctx, AuthenticatedUser{
		ID:            "user-invitee",
		Email:         "invitee@example.test",
		EmailVerified: false,
	}, ListMyProjectInvitationsInput{Limit: 50})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListMyProjectInvitations() unverified error = %v, want ErrForbidden", err)
	}

	page, err := service.ListMyProjectInvitations(ctx, AuthenticatedUser{
		ID:            "user-invitee",
		Email:         "INVITEE@example.test",
		EmailVerified: true,
	}, ListMyProjectInvitationsInput{Limit: 50})
	if err != nil {
		t.Fatalf("ListMyProjectInvitations() verified error = %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != "invitation-1" {
		t.Fatalf("ListMyProjectInvitations() IDs = %v, want [invitation-1]", invitationIDsForTest(page.Data))
	}
	if page.Meta.HasMore || page.Meta.NextCursor != nil {
		t.Fatalf("ListMyProjectInvitations() meta = %#v, want terminal page", page.Meta)
	}
}

func TestActorCollectionListMyProjectInvitationsUsesStableSessionAndEmailBoundCursor(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	now := createdAt.Add(time.Hour)
	store := NewMemoryStoreForTest(t)
	store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
	store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
	store.AddUserEmailForTest(t, "user-invitee", "invitee@example.test")
	for _, invitationID := range []string{"invitation-1", "invitation-2"} {
		store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
			ID:            invitationID,
			ProjectID:     "project-1",
			ProjectTitle:  "The White Port",
			InviterUserID: "user-owner",
			Email:         "invitee@example.test",
			EmailHash:     emailHashForInvitationTest("invitee@example.test"),
			Role:          RoleViewer,
			Status:        ProjectInvitationStatusPending,
			ExpiresAt:     now.Add(time.Hour),
			CreatedAt:     createdAt,
			UpdatedAt:     createdAt,
		})
	}
	service := NewService(store, StaticIDsForTest("", ""), StaticClockForTest(now))
	actor := AuthenticatedUser{ID: "user-invitee", Email: "invitee@example.test", EmailVerified: true}

	first, err := service.ListMyProjectInvitations(ctx, actor, ListMyProjectInvitationsInput{Limit: 1})
	if err != nil {
		t.Fatalf("ListMyProjectInvitations() first page error = %v", err)
	}
	if got := invitationIDsForTest(first.Data); !reflect.DeepEqual(got, []string{"invitation-2"}) {
		t.Fatalf("ListMyProjectInvitations() first page IDs = %v, want [invitation-2]", got)
	}
	if !first.Meta.HasMore || first.Meta.NextCursor == nil || *first.Meta.NextCursor == "" {
		t.Fatalf("ListMyProjectInvitations() first page meta = %#v, want a next cursor", first.Meta)
	}

	second, err := service.ListMyProjectInvitations(ctx, actor, ListMyProjectInvitationsInput{Limit: 1, Cursor: *first.Meta.NextCursor})
	if err != nil {
		t.Fatalf("ListMyProjectInvitations() second page error = %v", err)
	}
	if got := invitationIDsForTest(second.Data); !reflect.DeepEqual(got, []string{"invitation-1"}) {
		t.Fatalf("ListMyProjectInvitations() second page IDs = %v, want [invitation-1]", got)
	}
	if second.Meta.HasMore || second.Meta.NextCursor != nil {
		t.Fatalf("ListMyProjectInvitations() second page meta = %#v, want terminal page", second.Meta)
	}

	emailChanged := actor
	emailChanged.Email = "other@example.test"
	if _, err := service.ListMyProjectInvitations(ctx, emailChanged, ListMyProjectInvitationsInput{Limit: 1, Cursor: *first.Meta.NextCursor}); !errors.Is(err, ErrValidation) {
		t.Fatalf("ListMyProjectInvitations() changed-email cursor error = %v, want ErrValidation", err)
	}
	otherActor := actor
	otherActor.ID = "user-other"
	if _, err := service.ListMyProjectInvitations(ctx, otherActor, ListMyProjectInvitationsInput{Limit: 1, Cursor: *first.Meta.NextCursor}); !errors.Is(err, ErrValidation) {
		t.Fatalf("ListMyProjectInvitations() cross-session cursor error = %v, want ErrValidation", err)
	}
}

func TestMemoryStoreResolveProjectInvitationInviteeAccessRequiresMatchingVerifiedUser(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		userID  string
		arrange func(*testing.T, *memoryStoreForTest)
	}{
		{
			name:   "wrong actor",
			userID: "user-wrong",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.AddUserEmailForTest(t, "user-wrong", "wrong@example.test")
			},
		},
		{
			name:   "unknown actor",
			userID: "user-missing",
		},
		{
			name:   "unverified actor",
			userID: "user-unverified",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.AddUserEmailWithStateForTest(t, "user-unverified", "invitee@example.test", false, false)
			},
		},
		{
			name:   "disabled actor",
			userID: "user-disabled",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.AddUserEmailWithStateForTest(t, "user-disabled", "invitee@example.test", true, true)
			},
		},
		{
			name:   "deleted project",
			userID: "user-invitee",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.AddUserEmailForTest(t, "user-invitee", "invitee@example.test")
				store.SoftDeleteProjectForTest(t, "project-1", now)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
			store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
			invitation := store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
				ID:            "invitation-1",
				ProjectID:     "project-1",
				ProjectTitle:  "The White Port",
				InviterUserID: "user-owner",
				Email:         "invitee@example.test",
				EmailHash:     emailHashForInvitationTest("invitee@example.test"),
				Role:          RoleViewer,
				Status:        ProjectInvitationStatusPending,
				ExpiresAt:     now.Add(time.Hour),
				CreatedAt:     createdAt,
				UpdatedAt:     createdAt,
			})
			if tt.arrange != nil {
				tt.arrange(t, store)
			}

			_, err := store.ResolveProjectInvitationInviteeAccessForActor(ctx, ResolveProjectInvitationInviteeAccessStoreInput{
				ActorUserID:  tt.userID,
				InvitationID: invitation.ID,
				EmailHash:    invitation.EmailHash,
			})

			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("ResolveProjectInvitationInviteeAccessForActor() error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestActorCollectionMemoryStoreListProjectInvitationsForActorRequiresActiveVerifiedUser(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		userID  string
		arrange func(*testing.T, *memoryStoreForTest)
	}{
		{
			name:   "unknown actor",
			userID: "user-missing",
		},
		{
			name:   "unverified actor",
			userID: "user-unverified",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.AddUserEmailWithStateForTest(t, "user-unverified", "invitee@example.test", false, false)
			},
		},
		{
			name:   "disabled actor",
			userID: "user-disabled",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.AddUserEmailWithStateForTest(t, "user-disabled", "invitee@example.test", true, true)
			},
		},
		{
			name:   "deleted project",
			userID: "user-invitee",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.AddUserEmailForTest(t, "user-invitee", "invitee@example.test")
				store.SoftDeleteProjectForTest(t, "project-1", now)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
			store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
			invitation := store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
				ID:            "invitation-1",
				ProjectID:     "project-1",
				ProjectTitle:  "The White Port",
				InviterUserID: "user-owner",
				Email:         "invitee@example.test",
				EmailHash:     emailHashForInvitationTest("invitee@example.test"),
				Role:          RoleViewer,
				Status:        ProjectInvitationStatusPending,
				ExpiresAt:     now.Add(time.Hour),
				CreatedAt:     createdAt,
				UpdatedAt:     createdAt,
			})
			if tt.arrange != nil {
				tt.arrange(t, store)
			}

			invitations, err := store.ListProjectInvitationsForActor(ctx, ListProjectInvitationsForActorStoreInput{
				ActorUserID: tt.userID,
				EmailHash:   invitation.EmailHash,
				Limit:       20,
			})
			if err != nil {
				t.Fatalf("ListProjectInvitationsForActor() error = %v", err)
			}
			if len(invitations) != 0 {
				t.Fatalf("ListProjectInvitationsForActor() returned %d invitations, want 0", len(invitations))
			}
		})
	}
}

func TestMemoryStoreAcceptProjectInvitationRejectsHiddenInviteeWithoutMutation(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 24, 13, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		userID  string
		arrange func(*testing.T, *memoryStoreForTest)
	}{
		{
			name:   "wrong actor stored email mismatch",
			userID: "user-wrong",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.AddUserEmailForTest(t, "user-wrong", "wrong@example.test")
			},
		},
		{
			name:   "missing actor no stored user",
			userID: "user-missing",
		},
		{
			name:   "unverified actor",
			userID: "user-unverified",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.AddUserEmailWithStateForTest(t, "user-unverified", "invitee@example.test", false, false)
			},
		},
		{
			name:   "disabled actor",
			userID: "user-disabled",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.AddUserEmailWithStateForTest(t, "user-disabled", "invitee@example.test", true, true)
			},
		},
		{
			name:   "deleted project",
			userID: "user-invitee",
			arrange: func(t *testing.T, store *memoryStoreForTest) {
				store.AddUserEmailForTest(t, "user-invitee", "invitee@example.test")
				store.SoftDeleteProjectForTest(t, "project-1", now)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStoreForTest(t)
			store.AddProjectForTest(t, "project-1", "The White Port", "City notes", createdAt)
			store.AddMembershipForTest(t, "membership-owner", "project-1", "user-owner", RoleOwner, createdAt)
			invitation := store.AddProjectInvitationForTest(t, ProjectInvitationRecord{
				ID:            "invitation-1",
				ProjectID:     "project-1",
				ProjectTitle:  "The White Port",
				InviterUserID: "user-owner",
				Email:         "invitee@example.test",
				EmailHash:     emailHashForInvitationTest("invitee@example.test"),
				Role:          RoleViewer,
				Status:        ProjectInvitationStatusPending,
				ExpiresAt:     now.Add(time.Hour),
				CreatedAt:     createdAt,
				UpdatedAt:     createdAt,
			})
			if tt.arrange != nil {
				tt.arrange(t, store)
			}
			expectedInvitation := store.ProjectInvitationForTest(t, invitation.ID)
			membershipCount := len(store.membershipsByProjectIDAndUserID)

			_, err := store.AcceptProjectInvitation(ctx, AcceptProjectInvitationStoreInput{
				InvitationID: invitation.ID,
				MembershipID: "membership-accepted",
				UserID:       tt.userID,
				EmailHash:    invitation.EmailHash,
				AcceptedAt:   now,
			})

			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("AcceptProjectInvitation() error = %v, want ErrNotFound", err)
			}
			if _, ok := store.MembershipForTest("project-1", tt.userID); ok {
				t.Fatalf("AcceptProjectInvitation() created membership for %s", tt.userID)
			}
			if got := len(store.membershipsByProjectIDAndUserID); got != membershipCount {
				t.Fatalf("membership count = %d, want unchanged %d", got, membershipCount)
			}
			storedInvitation := store.ProjectInvitationForTest(t, invitation.ID)
			if storedInvitation.Status != ProjectInvitationStatusPending || storedInvitation.AcceptedAt != nil {
				t.Fatalf("stored invitation status/acceptedAt = %s/%v, want pending/<nil>", storedInvitation.Status, storedInvitation.AcceptedAt)
			}
			if storedInvitation != expectedInvitation {
				t.Fatalf("stored invitation = %#v, want unchanged %#v", storedInvitation, expectedInvitation)
			}
		})
	}
}

func emailHashForInvitationTest(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

func invitationIDsForTest(invitations []ProjectInvitationRecord) []string {
	ids := make([]string, len(invitations))
	for i, invitation := range invitations {
		ids[i] = invitation.ID
	}
	return ids
}
