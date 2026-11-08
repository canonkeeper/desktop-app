package projectaccess

import (
	"reflect"
	"testing"
)

func TestResolvedResourceAccessContainsOnlyOwningIdentityAndAuthorizationProof(t *testing.T) {
	t.Parallel()

	typeOf := reflect.TypeOf(ResolvedResourceAccess{})
	wantFields := map[string]bool{
		"ProjectID":   true,
		"Identity":    true,
		"ProjectRole": true,
		"Visibility":  true,
	}
	if typeOf.NumField() != len(wantFields) {
		t.Fatalf("ResolvedResourceAccess field count = %d, want %d", typeOf.NumField(), len(wantFields))
	}
	for i := 0; i < typeOf.NumField(); i++ {
		if !wantFields[typeOf.Field(i).Name] {
			t.Fatalf("ResolvedResourceAccess exposes non-contract field %q", typeOf.Field(i).Name)
		}
	}
}
