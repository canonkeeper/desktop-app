package jobstore

import (
	"reflect"
	"testing"
)

func TestWorkerClaimAuthorityExposesOnlyRegisteredDomainMethods(t *testing.T) {
	typeOfAuthority := reflect.TypeOf(&WorkerClaimAuthority{})
	if typeOfAuthority.NumMethod() != 5 {
		t.Fatalf("worker authority exposes %d methods, want five registered claims", typeOfAuthority.NumMethod())
	}
	for _, name := range []string{
		"ClaimNextQueuedJobForWorker",
		"ClaimNextDeletionTargetTaskForWorker",
		"ClaimNextSourceHardPurgeForWorker",
		"ClaimNextImportExpiryForWorker",
		"ClaimNextDeletionAuditGCForWorker",
	} {
		if method, ok := typeOfAuthority.MethodByName(name); !ok || method.Name == "" {
			t.Fatalf("worker authority is missing %s", name)
		}
	}
	valueType := typeOfAuthority.Elem()
	for index := 0; index < valueType.NumField(); index++ {
		field := valueType.Field(index)
		if field.IsExported() || field.Anonymous {
			t.Fatalf("worker authority exposes field %s", field.Name)
		}
		if field.Type.String() == "sqlc.DBTX" {
			t.Fatalf("worker authority exposes a raw DBTX field %s", field.Name)
		}
	}
}
