package shelf

import (
	"reflect"
	"testing"
)

// specs/2026-11-11-ux7-reorder.md RK1.6: the first of two books deleted, the one left is «Книга 1»; the stored orders
// with gaps and in any order give 1…n.
func TestThePlacesOfTheBooksGoWithoutGaps(t *testing.T) {
	if got := Places([]Book{{ID: "second", Order: 2}}); !reflect.DeepEqual(got, map[string]int{"second": 1}) {
		t.Fatalf("the one book left = %v", got)
	}
	got := Places([]Book{{ID: "c", Order: 7}, {ID: "a", Order: 2}, {ID: "b", Order: 2}, {ID: "d", Order: 4}})
	if want := map[string]int{"a": 1, "b": 2, "d": 3, "c": 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Places() = %v, want %v", got, want)
	}
	if got := Places(nil); len(got) != 0 {
		t.Fatalf("no books = %v", got)
	}
}
