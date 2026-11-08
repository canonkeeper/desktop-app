package library

import (
	"crypto/sha256"
	"testing"
	"time"
)

func TestActivateCoverInputValidatesPersistedMediaFacts(t *testing.T) {
	valid := ActivateCoverInput{
		Actor:         Actor{UserID: "00000000-0000-4000-8000-000000000001"},
		ArtifactID:    "00000000-0000-4000-8000-000000000002",
		BookID:        "00000000-0000-4000-8000-000000000003",
		StorageKey:    "book-covers/cover.final",
		MIMEType:      "image/jpeg",
		ByteSize:      1024,
		Width:         MinCoverWidth,
		Height:        MinCoverHeight,
		ContentSHA256: sha256.Sum256([]byte("cover")),
		At:            time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid input error = %v", err)
	}

	tests := map[string]func(*ActivateCoverInput){
		"missing actor": func(input *ActivateCoverInput) { input.Actor.UserID = "" },
		"missing key":   func(input *ActivateCoverInput) { input.StorageKey = "" },
		"unsupported MIME": func(input *ActivateCoverInput) {
			input.MIMEType = "image/gif"
		},
		"too large":  func(input *ActivateCoverInput) { input.ByteSize = MaxCoverBytes + 1 },
		"too narrow": func(input *ActivateCoverInput) { input.Width = MinCoverWidth - 1 },
		"too short":  func(input *ActivateCoverInput) { input.Height = MinCoverHeight - 1 },
		"zero digest": func(input *ActivateCoverInput) {
			input.ContentSHA256 = [sha256.Size]byte{}
		},
		"missing time": func(input *ActivateCoverInput) { input.At = time.Time{} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := valid
			mutate(&input)
			if err := input.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}
