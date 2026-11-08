package library

import (
	"bytes"
	"context"
	"image"

	domainlibrary "canonkeeper/backend/internal/domain/library"
)

// CoverBookReader reads a book for an actor.
type CoverBookReader interface {
	BookHead(context.Context, domainlibrary.Actor, string) (domainlibrary.BookHead, error)
}

// CoverImageRenders reads what a drawing of a project looks like, with its visible edits.
type CoverImageRenders interface {
	ImageRender(ctx context.Context, actor domainlibrary.Actor, projectID string, number int) ([]byte, error)
}

// SetCoverFromImageInput names a book and a drawing of its saga.
type SetCoverFromImageInput struct {
	Actor       domainlibrary.Actor
	BookID      string
	ImageNumber int
}

// SetCoverFromImage makes a drawing of the saga the cover of a book (specs/2026-11-05-voice-full-access.md A8.6): the cover
// takes a copy of the drawing by the path of an upload, so a later edit or deletion of the drawing leaves it as it is.
type SetCoverFromImage struct {
	books   CoverBookReader
	renders CoverImageRenders
	upload  *UploadCover
}

func NewSetCoverFromImage(books CoverBookReader, renders CoverImageRenders, upload *UploadCover) *SetCoverFromImage {
	if books == nil || renders == nil || upload == nil {
		panic("book cover from a drawing: dependencies are required")
	}
	return &SetCoverFromImage{books: books, renders: renders, upload: upload}
}

func (useCase *SetCoverFromImage) Execute(ctx context.Context, input SetCoverFromImageInput) (domainlibrary.CoverActivation, error) {
	if input.ImageNumber < 1 {
		return domainlibrary.CoverActivation{}, domainlibrary.ErrValidation
	}
	head, err := useCase.books.BookHead(ctx, input.Actor, input.BookID)
	if err != nil {
		return domainlibrary.CoverActivation{}, err
	}
	if !head.CanEdit {
		return domainlibrary.CoverActivation{}, domainlibrary.ErrForbidden
	}
	render, err := useCase.renders.ImageRender(ctx, input.Actor, head.ProjectID, input.ImageNumber)
	if err != nil {
		return domainlibrary.CoverActivation{}, err
	}
	content, err := withinCoverLimit(render)
	if err != nil {
		return domainlibrary.CoverActivation{}, err
	}
	return useCase.upload.Execute(ctx, UploadCoverInput{Actor: input.Actor, BookID: input.BookID, Content: content})
}

// withinCoverLimit leaves a render that fits the limit of a cover as it is, and makes JPEG of a larger one: a drawing
// enlarged twice takes more than 5 MiB as PNG, and JPEG of the same size is several times smaller.
func withinCoverLimit(render []byte) ([]byte, error) {
	if int64(len(render)) <= domainlibrary.MaxCoverBytes {
		return render, nil
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(render))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > maxConvertedCoverPixels/config.Height {
		return nil, domainlibrary.ErrCoverTooLarge
	}
	picture, _, err := image.Decode(bytes.NewReader(render))
	if err != nil {
		return nil, domainlibrary.ErrCoverTooLarge
	}
	converted, err := coverJPEG(picture)
	if err != nil || int64(len(converted)) > domainlibrary.MaxCoverBytes {
		return nil, domainlibrary.ErrCoverTooLarge
	}
	return converted, nil
}
