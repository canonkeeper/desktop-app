package search

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"math"
)

const (
	searchCursorBinaryLength  = 132
	searchCursorEncodedLength = 176
)

type searchCursor struct {
	RequestHash  [32]byte
	ManifestHash [32]byte
	RankBits     uint64
	BookID       [16]byte
	ChapterID    [16]byte
	ResultID     [16]byte
}

func encodeSearchCursor(cursor searchCursor) (string, error) {
	if !validSearchCursor(cursor) {
		return "", ErrValidation
	}
	payload := make([]byte, searchCursorBinaryLength)
	copy(payload[:12], SearchContractVersion)
	copy(payload[12:44], cursor.RequestHash[:])
	copy(payload[44:76], cursor.ManifestHash[:])
	binary.BigEndian.PutUint64(payload[76:84], cursor.RankBits)
	copy(payload[84:100], cursor.BookID[:])
	copy(payload[100:116], cursor.ChapterID[:])
	copy(payload[116:132], cursor.ResultID[:])
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeSearchCursor(encoded string, requestHash [32]byte) (searchCursor, error) {
	if len(encoded) != searchCursorEncodedLength {
		return searchCursor{}, ErrValidation
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(payload) != searchCursorBinaryLength ||
		!bytes.Equal(payload[:12], []byte(SearchContractVersion)) {
		return searchCursor{}, ErrValidation
	}
	var cursor searchCursor
	copy(cursor.RequestHash[:], payload[12:44])
	copy(cursor.ManifestHash[:], payload[44:76])
	cursor.RankBits = binary.BigEndian.Uint64(payload[76:84])
	copy(cursor.BookID[:], payload[84:100])
	copy(cursor.ChapterID[:], payload[100:116])
	copy(cursor.ResultID[:], payload[116:132])
	if cursor.RequestHash != requestHash || !validSearchCursor(cursor) {
		return searchCursor{}, ErrValidation
	}
	return cursor, nil
}

func validSearchCursor(cursor searchCursor) bool {
	rank := math.Float64frombits(cursor.RankBits)
	return cursor.RequestHash != ([32]byte{}) &&
		cursor.ManifestHash != ([32]byte{}) &&
		rank > 0 && !math.IsInf(rank, 0) && !math.IsNaN(rank) &&
		cursor.BookID != ([16]byte{}) && cursor.ChapterID != ([16]byte{}) && cursor.ResultID != ([16]byte{})
}
