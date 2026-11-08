package jobs

import "canonkeeper/backend/internal/governance"

func (input CanonTextIndexIntentInput) Validate() error {
	if !uuidShaped(input.JobID) || !uuidShaped(input.Subject.ProjectID) ||
		(input.CreatorUserID != "" && !uuidShaped(input.CreatorUserID)) || input.CreatedAt.IsZero() ||
		input.Subject.Type != KindCanonTextIndex || input.Subject.CatalogRevision == 0 ||
		input.Source.SourceView.Validate() != nil || len(input.Source.Refs) == 0 {
		return ErrValidation
	}
	view, present := input.Source.SourceView.View()
	if !present || governance.SourceView(view) != input.Subject.SourceView {
		return ErrValidation
	}
	if _, err := EncodeSubject(KindCanonTextIndex, input.Subject); err != nil {
		return err
	}
	_, err := governance.NewArtifactManifest(governance.ArtifactManifestInput{
		Kind: governance.ArtifactJob, ProjectID: input.Subject.ProjectID, ArtifactID: input.JobID,
		OwnerSourceView: input.Source.SourceView, Refs: input.Source.Refs,
	})
	if err != nil {
		return ErrValidation
	}
	return nil
}
