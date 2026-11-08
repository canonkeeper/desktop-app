package jobstore_test

import (
	"reflect"
	"testing"

	"canonkeeper/backend/internal/jobs"
	jobstore "canonkeeper/backend/internal/store/jobs"
)

func TestJobPersistenceContractsDoNotExposeGenericCreateJob(t *testing.T) {
	if _, ok := reflect.TypeOf((*jobs.Store)(nil)).Elem().MethodByName("CreateJob"); ok {
		t.Error("jobs.Store exposes generic CreateJob")
	}
	if _, ok := reflect.TypeOf((*jobstore.Store)(nil)).MethodByName("CreateJob"); ok {
		t.Error("*jobstore.Store exposes generic CreateJob")
	}
}
