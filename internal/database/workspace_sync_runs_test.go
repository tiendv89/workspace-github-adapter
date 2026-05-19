package database

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestWorkspaceSyncRunReferenceFieldsUseUUIDs(t *testing.T) {
	uuidType := reflect.TypeOf(pgtype.UUID{})

	runType := reflect.TypeOf(WorkspaceSyncRun{})
	for _, fieldName := range []string{"FeatureID", "TaskID"} {
		field, ok := runType.FieldByName(fieldName)
		if !ok {
			t.Fatalf("WorkspaceSyncRun missing %s", fieldName)
		}
		if field.Type != uuidType {
			t.Fatalf("WorkspaceSyncRun.%s type = %s, want %s", fieldName, field.Type, uuidType)
		}
	}

	paramsType := reflect.TypeOf(InsertSyncRunParams{})
	for _, fieldName := range []string{"FeatureID", "TaskID"} {
		field, ok := paramsType.FieldByName(fieldName)
		if !ok {
			t.Fatalf("InsertSyncRunParams missing %s", fieldName)
		}
		if field.Type != uuidType {
			t.Fatalf("InsertSyncRunParams.%s type = %s, want %s", fieldName, field.Type, uuidType)
		}
	}
}
