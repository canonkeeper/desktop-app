// SPDX-License-Identifier: AGPL-3.0-only

package door

import (
	"context"
	"errors"

	codexresearch "canonkeeper/backend/internal/delivery/codex/researchchat"
	"canonkeeper/backend/internal/domain/authormemory"
	"canonkeeper/backend/internal/domain/quota"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	researchservice "canonkeeper/backend/internal/services/researchchat"
)

// ChatParts are what the composition of the app gives the handler of a call of a program (specs/2026-11-15-desk-mcp.md
// MC2.4): the read authority of the local author in a saga without a turn (the authority of the voice: a member of the saga,
// the published text, no manifest, the revisions checked), the memory of the author read by ListAuthorMemory, and the stores
// of the chat — the same the chat of the app reads with.
type ChatParts struct {
	Reads     func(projectID string) researchservice.ReadRepository
	Memory    func(ctx context.Context, projectID string, filter authormemory.Filter) (authormemory.Page, error)
	Saga      researchservice.SagaText
	Layer     researchservice.ThreadLayer
	Materials researchservice.MaterialsReader
	Chapters  researchservice.ChapterVersions
}

// ChatHandlers builds the handler of the chat for every call afresh, naming what it shows to the naming of the call. Nothing
// it reads can call a model: the model of the scan refuses and goes to no gateway; nothing can change the saga: the writing
// of the memory refuses.
func ChatHandlers(parts ChatParts) Handlers {
	if parts.Reads == nil || parts.Memory == nil || parts.Saga == nil || parts.Layer == nil || parts.Materials == nil || parts.Chapters == nil {
		panic("door: the parts of the handler of the chat are required")
	}
	return func(projectID string, naming researchservice.Naming) Handler {
		memory := programMemory{reads: parts.Reads(projectID), list: parts.Memory, projectID: projectID}
		reads := researchservice.NewReadTools(memory.reads, parts.Saga, noModel{}, nil, researchservice.ScanSettings{}, parts.Layer).
			WithMaterials(parts.Materials).WithAuthorMemory(memory).WithNaming(naming)
		return codexresearch.NewHandler(reads, researchservice.NewAuthorMemoryTools(memory, parts.Chapters).WithNaming(naming)).Handle
	}
}

// errNoModel is the model of a reading of a program: a program asks for no work of a model of the app.
var errNoModel = errors.New("door: a program of the author calls no model of the app")

type noModel struct{}

func (noModel) Complete(context.Context, string, string) (string, quota.ModelUsage, error) {
	return "", quota.ModelUsage{}, errNoModel
}

// programMemory is the memory of the author without a turn: read by ListAuthorMemory as a member of the saga — a saga that is
// not his or is deleted is the refusal of a reading —, never written.
type programMemory struct {
	reads     researchservice.ReadRepository
	list      func(ctx context.Context, projectID string, filter authormemory.Filter) (authormemory.Page, error)
	projectID string
}

func (memory programMemory) ReadAuthority(ctx context.Context, call researchservice.ToolContext) (researchdomain.ReadAuthority, error) {
	return memory.reads.ReadAuthority(ctx, call)
}

func (memory programMemory) RememberForTurn(context.Context, researchservice.ToolContext, researchdomain.ReadAuthority, authormemory.Draft) error {
	return researchdomain.ErrForbidden
}

func (memory programMemory) AuthorMemoryForTurn(
	ctx context.Context, _ researchservice.ToolContext, _ researchdomain.ReadAuthority, filter authormemory.Filter,
) (authormemory.Page, error) {
	page, err := memory.list(ctx, memory.projectID, filter)
	if errors.Is(err, authormemory.ErrNotFound) {
		return authormemory.Page{}, researchdomain.ErrForbidden
	}
	return page, err
}
