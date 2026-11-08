package researchchat

import (
	"encoding/json"
	"testing"
)

func TestSessionPageAlwaysSerializesNullableNextCursor(t *testing.T) {
	encoded, err := json.Marshal(SessionPage{Data: []Session{}, Meta: PageMeta{HasMore: false}})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"data":[],"meta":{"hasMore":false,"nextCursor":null}}` {
		t.Fatalf("session page JSON = %s", encoded)
	}
}

// specs/2026-10-29-saga-illustrations.md R15.17: the anchor of an attached image is its place in the list.
func TestPhotoNameIsThePlaceOfTheImage(t *testing.T) {
	if PhotoName(2) != "фото 2" {
		t.Fatalf("PhotoName(2) = %q", PhotoName(2))
	}
}
