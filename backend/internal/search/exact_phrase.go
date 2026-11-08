package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"unicode/utf8"

	"canonkeeper/backend/internal/governance"
)

const (
	exactPhraseCursorBinaryLength  = 88
	exactPhraseCursorEncodedLength = 118
	exactPhraseRequestHashDomain   = "CKCanonicalExactPhraseRequestV2\x00"
)

var exactPhraseCursorMagic = [4]byte{'C', 'K', 'E', '3'}

type ExactPhraseRequest struct {
	ProjectID  string
	SourceView governance.SourceView
	BookID     string
	ChapterID  string
	Query      string
	Limit      int
	Cursor     string
}

type ExactPhraseResponse struct {
	Items      []ExactPhraseResult
	NextCursor string
	Truncated  bool
}

type ExactPhraseResult struct {
	ChapterOrder int32
	ChapterID    string
	StartByte    uint32
	EndByte      uint32
	SourceRef    string
}

type exactPhraseCursor struct {
	RequestHash  [32]byte
	ManifestHash [32]byte
	ChapterOrder int32
	ChapterID    [16]byte
}

func (service *Service) SearchExactPhrase(ctx context.Context, actor Actor, request ExactPhraseRequest) (ExactPhraseResponse, error) {
	normalized, err := normalizeExactPhraseRequest(request)
	if err != nil {
		return ExactPhraseResponse{}, err
	}
	actorID, _, err := canonicalUUID(actor.UserID)
	if err != nil {
		return ExactPhraseResponse{}, ErrNotFound
	}
	requestHash := exactPhraseRequestHash(normalized)
	var decoded *exactPhraseCursor
	if normalized.Cursor != "" {
		value, err := decodeExactPhraseCursor(normalized.Cursor, requestHash)
		if err != nil {
			return ExactPhraseResponse{}, err
		}
		decoded = &value
	}
	input := ExactPhraseSearchInput{
		ActorUserID: actorID, ProjectID: normalized.ProjectID, SourceView: normalized.SourceView,
		BookID: normalized.BookID, ChapterID: normalized.ChapterID, Query: normalized.Query, Limit: normalized.Limit,
	}
	if decoded != nil {
		input.ExpectedManifestHash = &decoded.ManifestHash
		input.Cursor = &ExactPhraseCursor{
			ChapterOrder: decoded.ChapterOrder, ChapterID: canonicalUUIDString(decoded.ChapterID),
		}
	}
	output, err := service.store.SearchExactPhrase(ctx, input)
	if err != nil {
		return ExactPhraseResponse{}, mapSearchError(err)
	}
	return exactPhraseResponse(normalized, requestHash, output)
}

func normalizeExactPhraseRequest(input ExactPhraseRequest) (ExactPhraseRequest, error) {
	var err error
	input.ProjectID, _, err = canonicalUUID(input.ProjectID)
	if err != nil {
		return ExactPhraseRequest{}, ErrValidation
	}
	input.BookID, _, err = canonicalUUID(input.BookID)
	if err != nil {
		return ExactPhraseRequest{}, ErrValidation
	}
	if input.ChapterID != "" {
		input.ChapterID, _, err = canonicalUUID(input.ChapterID)
		if err != nil {
			return ExactPhraseRequest{}, ErrValidation
		}
	}
	if input.SourceView != governance.SourceViewDraft && input.SourceView != governance.SourceViewPublished {
		return ExactPhraseRequest{}, ErrValidation
	}
	input.Query, _ = NormalizeExactPhrase(input.Query)
	if input.Query == "" || utf8.RuneCountInString(input.Query) > maxQueryRunes {
		return ExactPhraseRequest{}, ErrValidation
	}
	if input.Limit == 0 {
		input.Limit = defaultSearchLimit
	}
	if input.Limit < 1 || input.Limit > maxSearchLimit || len(input.Cursor) > exactPhraseCursorEncodedLength {
		return ExactPhraseRequest{}, ErrValidation
	}
	return input, nil
}

func exactPhraseRequestHash(input ExactPhraseRequest) [32]byte {
	projection := []byte(exactPhraseRequestHashDomain)
	for _, field := range [][]byte{
		[]byte(input.ProjectID), []byte(input.SourceView), []byte(input.BookID), []byte(input.ChapterID), []byte(input.Query),
	} {
		projection = appendLengthPrefixed(projection, field)
	}
	var limit [4]byte
	binary.BigEndian.PutUint32(limit[:], uint32(input.Limit))
	projection = append(projection, limit[:]...)
	return sha256.Sum256(projection)
}

func encodeExactPhraseCursor(cursor exactPhraseCursor) (string, error) {
	if !validExactPhraseCursor(cursor) {
		return "", ErrValidation
	}
	payload := make([]byte, exactPhraseCursorBinaryLength)
	copy(payload[0:4], exactPhraseCursorMagic[:])
	copy(payload[4:36], cursor.RequestHash[:])
	copy(payload[36:68], cursor.ManifestHash[:])
	binary.BigEndian.PutUint32(payload[68:72], uint32(cursor.ChapterOrder))
	copy(payload[72:88], cursor.ChapterID[:])
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeExactPhraseCursor(encoded string, requestHash [32]byte) (exactPhraseCursor, error) {
	if len(encoded) != exactPhraseCursorEncodedLength {
		return exactPhraseCursor{}, ErrValidation
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(payload) != exactPhraseCursorBinaryLength || !bytes.Equal(payload[:4], exactPhraseCursorMagic[:]) {
		return exactPhraseCursor{}, ErrValidation
	}
	var cursor exactPhraseCursor
	copy(cursor.RequestHash[:], payload[4:36])
	copy(cursor.ManifestHash[:], payload[36:68])
	cursor.ChapterOrder = int32(binary.BigEndian.Uint32(payload[68:72]))
	copy(cursor.ChapterID[:], payload[72:88])
	if cursor.RequestHash != requestHash || !validExactPhraseCursor(cursor) {
		return exactPhraseCursor{}, ErrValidation
	}
	return cursor, nil
}

func validExactPhraseCursor(cursor exactPhraseCursor) bool {
	return cursor.RequestHash != ([32]byte{}) && cursor.ManifestHash != ([32]byte{}) &&
		cursor.ChapterOrder >= 0 && cursor.ChapterID != ([16]byte{})
}

func exactPhraseResponse(request ExactPhraseRequest, requestHash [32]byte, output ExactPhraseSearchOutput) (ExactPhraseResponse, error) {
	if output.ManifestHash == ([32]byte{}) || len(output.Matches) > request.Limit || output.Truncated != (output.NextCursor != nil) {
		return ExactPhraseResponse{}, ErrCorrupt
	}
	response := ExactPhraseResponse{Items: make([]ExactPhraseResult, len(output.Matches)), Truncated: output.Truncated}
	for index, match := range output.Matches {
		if match.ChapterOrder < 0 || match.StartByte >= match.EndByte || match.SourceRef != match.ChapterID {
			return ExactPhraseResponse{}, ErrCorrupt
		}
		if _, _, err := canonicalUUID(match.ChapterID); err != nil {
			return ExactPhraseResponse{}, ErrCorrupt
		}
		response.Items[index] = ExactPhraseResult{
			ChapterOrder: match.ChapterOrder, ChapterID: match.ChapterID,
			StartByte: match.StartByte, EndByte: match.EndByte, SourceRef: match.SourceRef,
		}
	}
	if output.NextCursor != nil {
		_, chapterID, chapterErr := canonicalUUID(output.NextCursor.ChapterID)
		if chapterErr != nil {
			return ExactPhraseResponse{}, ErrCorrupt
		}
		var err error
		response.NextCursor, err = encodeExactPhraseCursor(exactPhraseCursor{
			RequestHash: requestHash, ManifestHash: output.ManifestHash,
			ChapterOrder: output.NextCursor.ChapterOrder, ChapterID: chapterID,
		})
		if err != nil {
			return ExactPhraseResponse{}, ErrCorrupt
		}
	}
	return response, nil
}
