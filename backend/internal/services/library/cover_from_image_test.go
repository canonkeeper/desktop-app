package library

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"testing"

	domainlibrary "canonkeeper/backend/internal/domain/library"
)

type coverHeadFake struct {
	head domainlibrary.BookHead
	err  error
}

func (fake coverHeadFake) BookHead(context.Context, domainlibrary.Actor, string) (domainlibrary.BookHead, error) {
	return fake.head, fake.err
}

type coverRenderFake struct {
	content []byte
	err     error
	asked   *[]string
}

func (fake coverRenderFake) ImageRender(_ context.Context, actor domainlibrary.Actor, projectID string, number int) ([]byte, error) {
	if fake.asked != nil {
		*fake.asked = append(*fake.asked, actor.UserID+"/"+projectID+"/"+string(rune('0'+number)))
	}
	return fake.content, fake.err
}

func coverFromImage(head coverHeadFake, render coverRenderFake) (*SetCoverFromImage, *coverObjectsForTest, *coverMetadataStoreForTest) {
	objects := newCoverObjectsForTest()
	metadata := &coverMetadataStoreForTest{activationVersion: 1}
	return NewSetCoverFromImage(head, render, NewUploadCover(metadata, objects, fixedCoverIdentity)), objects, metadata
}

var coverInput = SetCoverFromImageInput{Actor: domainlibrary.Actor{UserID: "actor"}, BookID: "book", ImageNumber: 7}

// A8.6: the render of a drawing of the saga of the book becomes its cover by the path of an upload: the same checks and
// the same storage, with the bytes of the drawing as they are when they fit.
func TestCoverFromImageTakesTheRenderOfTheDrawing(t *testing.T) {
	render := encodedPNG(t, 1024, 1536)
	asked := []string{}
	useCase, objects, metadata := coverFromImage(
		coverHeadFake{head: domainlibrary.BookHead{ProjectID: "project", CanEdit: true}},
		coverRenderFake{content: render, asked: &asked},
	)
	activation, err := useCase.Execute(context.Background(), coverInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "actor/project/7" {
		t.Fatalf("asked = %v", asked)
	}
	if activation.Current.Width != 1024 || activation.Current.Height != 1536 || metadata.activated.MIMEType != "image/png" || metadata.activated.BookID != "book" {
		t.Fatalf("activation = %+v, activated = %+v", activation.Current, metadata.activated)
	}
	if !bytes.Equal(objects.values[fixedFinalKey], render) {
		t.Fatal("the cover is not the render of the drawing")
	}
}

// noisyPNG is a picture that does not compress: a render above the limit of a cover.
func noisyPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	random := rand.New(rand.NewSource(1))
	for y := range height {
		for x := range width {
			picture.Set(x, y, color.RGBA{R: uint8(random.Intn(256)), G: uint8(random.Intn(256)), B: uint8(random.Intn(256)), A: 255})
		}
	}
	var content bytes.Buffer
	if err := png.Encode(&content, picture); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}

// A drawing enlarged twice is larger than 5 MiB as PNG, and the author is not to be told to shrink what he cannot touch.
func TestCoverFromImageFitsALargeRenderIntoTheLimit(t *testing.T) {
	render := noisyPNG(t, 1400, 2100)
	if int64(len(render)) <= domainlibrary.MaxCoverBytes {
		t.Fatalf("the render of the test takes only %d bytes", len(render))
	}
	useCase, objects, metadata := coverFromImage(
		coverHeadFake{head: domainlibrary.BookHead{ProjectID: "project", CanEdit: true}},
		coverRenderFake{content: render},
	)
	activation, err := useCase.Execute(context.Background(), coverInput)
	if err != nil {
		t.Fatal(err)
	}
	stored := objects.values[fixedFinalKey]
	if metadata.activated.MIMEType != "image/jpeg" || activation.Current.Width != 1400 || activation.Current.Height != 2100 || int64(len(stored)) > domainlibrary.MaxCoverBytes {
		t.Fatalf("activation = %+v, %d bytes of %s", activation.Current, len(stored), metadata.activated.MIMEType)
	}
}

// A8.6, rights and refusals: a reader changes nothing and reads nothing for it, a stranger does not see the book, a
// drawing not ready or absent has no cover, a small one is refused by the rule of every cover.
func TestCoverFromImageRefuses(t *testing.T) {
	editor := coverHeadFake{head: domainlibrary.BookHead{ProjectID: "project", CanEdit: true}}
	gone := errors.New("no such drawing")
	for name, test := range map[string]struct {
		head   coverHeadFake
		render coverRenderFake
		input  SetCoverFromImageInput
		want   error
	}{
		"a reader":        {head: coverHeadFake{head: domainlibrary.BookHead{ProjectID: "project"}}, want: domainlibrary.ErrForbidden, input: coverInput},
		"a stranger":      {head: coverHeadFake{err: domainlibrary.ErrNotFound}, want: domainlibrary.ErrNotFound, input: coverInput},
		"no such drawing": {head: editor, render: coverRenderFake{err: gone}, want: gone, input: coverInput},
		"too small":       {head: editor, render: coverRenderFake{content: encodedPNG(t, 600, 899)}, want: domainlibrary.ErrCoverTooSmall, input: coverInput},
		"not a number":    {head: editor, want: domainlibrary.ErrValidation, input: SetCoverFromImageInput{Actor: coverInput.Actor, BookID: "book"}},
		"not a picture":   {head: editor, render: coverRenderFake{content: bytes.Repeat([]byte("x"), int(domainlibrary.MaxCoverBytes)+1)}, want: domainlibrary.ErrCoverTooLarge, input: coverInput},
	} {
		t.Run(name, func(t *testing.T) {
			asked := []string{}
			test.render.asked = &asked
			useCase, objects, _ := coverFromImage(test.head, test.render)
			if _, err := useCase.Execute(context.Background(), test.input); !errors.Is(err, test.want) {
				t.Fatalf("err = %v, want %v", err, test.want)
			}
			if len(objects.values) != 0 {
				t.Fatal("a refusal stored a cover")
			}
			if name == "a reader" && len(asked) != 0 {
				t.Fatal("the drawing was read for a reader")
			}
		})
	}
}
