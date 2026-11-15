// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"

	library "canonkeeper/backend/internal/domain/library"

	"golang.org/x/image/webp"
)

// Picture is a cover of a file the saga takes (SF3.9).
type Picture struct {
	MIME   string
	Width  int
	Height int
}

// CoverPicture says whether a cover of a file is taken: its type by its content is the one its book names, its sides are
// those of an upload of a cover and it unpacks into no more than MaxCoverPixels. A cover not taken leaves its book without
// one; it never stops the saga.
func CoverPicture(content []byte, mime string) (Picture, bool) {
	if len(content) == 0 || int64(len(content)) > MaxCoverBytes || contentType(content) != mime {
		return Picture{}, false
	}
	var config image.Config
	var err error
	switch mime {
	case "image/jpeg":
		config, err = jpeg.DecodeConfig(bytes.NewReader(content))
	case "image/png":
		config, err = png.DecodeConfig(bytes.NewReader(content))
	case "image/webp":
		config, err = webp.DecodeConfig(bytes.NewReader(content))
	default:
		return Picture{}, false
	}
	if err != nil || config.Width < library.MinCoverWidth || config.Height < library.MinCoverHeight ||
		int64(config.Width)*int64(config.Height) > MaxCoverPixels {
		return Picture{}, false
	}
	return Picture{MIME: mime, Width: config.Width, Height: config.Height}, true
}

// contentType names a picture by its first bytes, as the upload of a cover does.
func contentType(content []byte) string {
	switch {
	case bytes.HasPrefix(content, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case bytes.HasPrefix(content, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case len(content) >= 12 && bytes.Equal(content[:4], []byte("RIFF")) && bytes.Equal(content[8:12], []byte("WEBPVP")[:4]):
		return "image/webp"
	}
	return ""
}
