package governance

import "errors"

type ArtifactKind string

const (
	ArtifactJob                ArtifactKind = "job"
	ArtifactImportUpload       ArtifactKind = "import_upload"
	ArtifactImportJobRequest   ArtifactKind = "import_job_request"
	ArtifactChatTurn           ArtifactKind = "chat_turn"
	ArtifactAgentWorkflowInput ArtifactKind = "agent_workflow_input"
)

var artifactKinds = []ArtifactKind{
	ArtifactJob,
	ArtifactImportUpload,
	ArtifactImportJobRequest,
	ArtifactChatTurn,
	ArtifactAgentWorkflowInput,
}

func ArtifactKinds() []ArtifactKind {
	return append([]ArtifactKind(nil), artifactKinds...)
}

func (kind ArtifactKind) tag() (byte, bool) {
	// Tags of removed kinds stay retired: 2, 10, 11, 12, 13 and 14.
	switch kind {
	case ArtifactJob:
		return 3, true
	case ArtifactImportUpload:
		return 4, true
	case ArtifactImportJobRequest:
		return 5, true
	case ArtifactChatTurn:
		return 6, true
	case ArtifactAgentWorkflowInput:
		return 16, true
	}
	return 0, false
}

type ChatArm struct {
	present   bool
	projectID string
	chatID    string
	epoch     ChatLifecycleEpoch
}

func AbsentChatArm() ChatArm {
	return ChatArm{}
}

func PresentChatArm(projectID, chatID string, epoch ChatLifecycleEpoch) (ChatArm, error) {
	arm := ChatArm{present: true, projectID: projectID, chatID: chatID, epoch: epoch}
	if err := arm.Validate(); err != nil {
		return ChatArm{}, err
	}
	return arm, nil
}

func (arm ChatArm) Validate() error {
	if !arm.present {
		if arm.projectID != "" || arm.chatID != "" || arm.epoch != 0 {
			return errors.New("governance: absent chat arm carries a value")
		}
		return nil
	}
	if _, err := parseCanonicalUUID(arm.projectID); err != nil {
		return errors.New("governance: invalid chat arm project")
	}
	if _, err := parseCanonicalUUID(arm.chatID); err != nil || arm.epoch == 0 {
		return errors.New("governance: invalid chat arm lifecycle")
	}
	return nil
}

type artifactRefArm uint8

const (
	artifactRefProject     artifactRefArm = 2
	artifactRefBook        artifactRefArm = 3
	artifactRefChapter     artifactRefArm = 4
	artifactRefChapterText artifactRefArm = 5
)

type ChapterSourceRefInput struct {
	Source                 SourceGenerationVector
	SourceRevision         uint64
	SourceView             SourceViewArm
	SourceGenerationDigest Digest
	ContentHash            Digest
	Chat                   ChatArm
}

type SourceIdentityState string

const (
	SourceIdentityScopeOnly          SourceIdentityState = "scope_only"
	SourceIdentityTextDerivedCurrent SourceIdentityState = "text_derived_current"
)

type ArtifactSourceRef struct {
	arm                           artifactRefArm
	projectID                     string
	source                        SourceGenerationVector
	sourceRevision                uint64
	sourceView                    SourceViewArm
	contentHash                   [32]byte
	locatorSourceGenerationDigest [32]byte
	textIdentityState             SourceIdentityState
	chat                          ChatArm
	sourceIndependentProject      bool
}

type ArtifactSourceRefKind string

const (
	ArtifactSourceRefScope   ArtifactSourceRefKind = "scope"
	ArtifactSourceRefChapter ArtifactSourceRefKind = "chapter"
)

type ArtifactSourceRefFields struct {
	RefKind                       ArtifactSourceRefKind
	ScopeKind                     string
	ProjectID                     string
	ProjectGeneration             LifecycleGeneration
	BookID                        string
	BookGeneration                LifecycleGeneration
	ChapterID                     string
	ChapterGeneration             LifecycleGeneration
	SourceRevision                uint64
	SourceView                    SourceViewArm
	ContentHash                   [32]byte
	LocatorSourceGenerationDigest [32]byte
	TextIdentityState             SourceIdentityState
	SourceIndependent             bool
	ChatID                        string
	ChatLifecycleEpoch            ChatLifecycleEpoch
}

func (ref ArtifactSourceRef) Fields() (ArtifactSourceRefFields, error) {
	if _, err := ArtifactSourceRefProjection(ref); err != nil {
		return ArtifactSourceRefFields{}, err
	}
	fields := ArtifactSourceRefFields{
		ProjectID:                     ref.source.ProjectID,
		ProjectGeneration:             ref.source.ProjectGeneration,
		BookID:                        ref.source.BookID,
		BookGeneration:                ref.source.BookGeneration,
		ChapterID:                     ref.source.ChapterID,
		ChapterGeneration:             ref.source.ChapterGeneration,
		SourceRevision:                ref.sourceRevision,
		SourceView:                    ref.sourceView,
		ContentHash:                   ref.contentHash,
		LocatorSourceGenerationDigest: ref.locatorSourceGenerationDigest,
		TextIdentityState:             ref.textIdentityState,
		SourceIndependent:             ref.sourceIndependentProject,
	}
	if ref.chat.present {
		fields.ChatID = ref.chat.chatID
		fields.ChatLifecycleEpoch = ref.chat.epoch
	}
	switch ref.arm {
	case artifactRefProject:
		fields.RefKind = ArtifactSourceRefScope
		fields.ScopeKind = "project"
	case artifactRefBook:
		fields.RefKind = ArtifactSourceRefScope
		fields.ScopeKind = "book"
	case artifactRefChapter:
		fields.RefKind = ArtifactSourceRefScope
		fields.ScopeKind = "chapter"
	case artifactRefChapterText:
		fields.RefKind = ArtifactSourceRefChapter
	default:
		return ArtifactSourceRefFields{}, errors.New("governance: unknown artifact source ref arm")
	}
	return fields, nil
}

func NewSourceIndependentProjectRef(vector SourceGenerationVector, chat ChatArm) (ArtifactSourceRef, error) {
	ref := ArtifactSourceRef{
		arm: artifactRefProject, projectID: vector.ProjectID, source: vector,
		sourceView: AbsentSourceView(), chat: chat, sourceIndependentProject: true,
	}
	if _, err := ArtifactSourceRefProjection(ref); err != nil {
		return ArtifactSourceRef{}, err
	}
	return ref, nil
}

func NewViewBoundProjectRef(vector SourceGenerationVector, view SourceViewArm, chat ChatArm) (ArtifactSourceRef, error) {
	ref := ArtifactSourceRef{arm: artifactRefProject, projectID: vector.ProjectID, source: vector, sourceView: view, chat: chat}
	if _, err := ArtifactSourceRefProjection(ref); err != nil {
		return ArtifactSourceRef{}, err
	}
	return ref, nil
}

func NewBookArtifactRef(vector SourceGenerationVector, view SourceViewArm, chat ChatArm) (ArtifactSourceRef, error) {
	ref := ArtifactSourceRef{arm: artifactRefBook, projectID: vector.ProjectID, source: vector, sourceView: view, chat: chat}
	if _, err := ArtifactSourceRefProjection(ref); err != nil {
		return ArtifactSourceRef{}, err
	}
	return ref, nil
}

func NewChapterArtifactRef(vector SourceGenerationVector, view SourceViewArm, chat ChatArm) (ArtifactSourceRef, error) {
	ref := ArtifactSourceRef{arm: artifactRefChapter, projectID: vector.ProjectID, source: vector, sourceView: view, chat: chat}
	if _, err := ArtifactSourceRefProjection(ref); err != nil {
		return ArtifactSourceRef{}, err
	}
	return ref, nil
}

func NewExactChapterArtifactRef(input ChapterSourceRefInput) (ArtifactSourceRef, error) {
	generation, err := SourceGenerationDigest(input.Source)
	if err != nil || generation != input.SourceGenerationDigest || input.SourceRevision == 0 || input.ContentHash == (Digest{}) {
		return ArtifactSourceRef{}, errors.New("governance: invalid exact chapter source ref")
	}
	ref := ArtifactSourceRef{
		arm: artifactRefChapterText, projectID: input.Source.ProjectID, source: input.Source,
		sourceRevision: input.SourceRevision, sourceView: input.SourceView,
		contentHash: [32]byte(input.ContentHash), locatorSourceGenerationDigest: [32]byte(input.SourceGenerationDigest),
		textIdentityState: SourceIdentityTextDerivedCurrent, chat: input.Chat,
	}
	if _, err := ArtifactSourceRefProjection(ref); err != nil {
		return ArtifactSourceRef{}, err
	}
	return ref, nil
}

func ArtifactSourceRefProjection(ref ArtifactSourceRef) ([]byte, error) {
	if err := ref.sourceView.Validate(); err != nil || ref.projectID == "" || ref.source.ProjectID != ref.projectID {
		return nil, errors.New("governance: invalid artifact source ref")
	}
	if err := ref.chat.Validate(); err != nil || (ref.chat.present && ref.chat.projectID != ref.projectID) {
		return nil, errors.New("governance: artifact source ref has a foreign chat")
	}
	projection := []byte{byte(ref.arm)}
	switch ref.arm {
	case artifactRefProject:
		if ref.textIdentityState != "" || ref.contentHash != ([32]byte{}) {
			return nil, errors.New("governance: project scope carries text identity")
		}
		kind, err := ref.source.Kind()
		if err != nil || kind != SourceEntityProject {
			return nil, errors.New("governance: invalid project scope ref")
		}
		if ref.sourceIndependentProject {
			if ref.sourceView.IsPresent() {
				return nil, errors.New("governance: source-independent project ref has a view")
			}
		} else if !ref.sourceView.IsPresent() {
			return nil, errors.New("governance: view-bound project ref has no view")
		}
		projection, err = appendSourceVectorRaw(projection, ref.source, SourceEntityProject)
		if err != nil {
			return nil, err
		}
		projection = appendSourceViewArm(projection, ref.sourceView)
	case artifactRefBook:
		if ref.textIdentityState != "" || ref.contentHash != ([32]byte{}) {
			return nil, errors.New("governance: book scope carries text identity")
		}
		kind, err := ref.source.Kind()
		if err != nil || kind != SourceEntityBook || !ref.sourceView.IsPresent() || ref.sourceIndependentProject {
			return nil, errors.New("governance: invalid book scope ref")
		}
		projection, err = appendSourceVectorRaw(projection, ref.source, SourceEntityBook)
		if err != nil {
			return nil, err
		}
		projection = appendSourceViewArm(projection, ref.sourceView)
	case artifactRefChapter:
		if ref.textIdentityState != "" || ref.contentHash != ([32]byte{}) {
			return nil, errors.New("governance: chapter scope carries text identity")
		}
		kind, err := ref.source.Kind()
		if err != nil || kind != SourceEntityChapter || !ref.sourceView.IsPresent() || ref.sourceIndependentProject {
			return nil, errors.New("governance: invalid chapter scope ref")
		}
		projection, err = appendSourceVectorRaw(projection, ref.source, SourceEntityChapter)
		if err != nil {
			return nil, err
		}
		projection = appendSourceViewArm(projection, ref.sourceView)
	case artifactRefChapterText:
		kind, err := ref.source.Kind()
		sourceDigest, digestErr := SourceGenerationDigest(ref.source)
		if err != nil || kind != SourceEntityChapter || digestErr != nil || !ref.sourceView.IsPresent() ||
			ref.sourceIndependentProject || ref.sourceRevision == 0 || ref.textIdentityState != SourceIdentityTextDerivedCurrent ||
			ref.contentHash == ([32]byte{}) || sourceDigest != Digest(ref.locatorSourceGenerationDigest) {
			return nil, errors.New("governance: invalid exact chapter source ref")
		}
		projection, err = appendSourceVectorRaw(projection, ref.source, SourceEntityChapter)
		if err != nil {
			return nil, err
		}
		projection = appendU64(projection, ref.sourceRevision)
		projection = appendSourceViewArm(projection, ref.sourceView)
		projection = append(projection, ref.locatorSourceGenerationDigest[:]...)
		projection = append(projection, ref.contentHash[:]...)
	default:
		return nil, errors.New("governance: unknown artifact source ref arm")
	}
	projection, err := appendChatArm(projection, ref.chat)
	if err != nil {
		return nil, err
	}
	return projection, nil
}

func appendSourceVectorRaw(target []byte, vector SourceGenerationVector, want SourceEntityKind) ([]byte, error) {
	kind, err := vector.Kind()
	if err != nil || kind != want {
		return nil, errors.New("governance: source vector scope mismatch")
	}
	projectID, _ := parseCanonicalUUID(vector.ProjectID)
	target = append(target, projectID[:]...)
	target = appendU64(target, uint64(vector.ProjectGeneration))
	if want == SourceEntityBook || want == SourceEntityChapter {
		bookID, _ := parseCanonicalUUID(vector.BookID)
		target = append(target, bookID[:]...)
		target = appendU64(target, uint64(vector.BookGeneration))
	}
	if want == SourceEntityChapter {
		chapterID, _ := parseCanonicalUUID(vector.ChapterID)
		target = append(target, chapterID[:]...)
		target = appendU64(target, uint64(vector.ChapterGeneration))
	}
	return target, nil
}

func appendSourceViewArm(target []byte, arm SourceViewArm) []byte {
	if !arm.present {
		return append(target, 0)
	}
	tag := byte(0)
	if arm.view == SourceViewDraft {
		tag = 1
	} else if arm.view == SourceViewPublished {
		tag = 2
	}
	return append(target, 1, tag)
}

func appendChatArm(target []byte, arm ChatArm) ([]byte, error) {
	if err := arm.Validate(); err != nil {
		return nil, err
	}
	if !arm.present {
		return append(target, 0), nil
	}
	chatID, _ := parseCanonicalUUID(arm.chatID)
	target = append(target, 1)
	target = append(target, chatID[:]...)
	target = appendU64(target, uint64(arm.epoch))
	return target, nil
}
