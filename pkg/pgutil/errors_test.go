package pgutil

import (
	"errors"
	"testing"
)

type errMsg struct{ msg string }

func (e *errMsg) Error() string { return e.msg }

func TestIsDedupeError_TaskAlreadyExists(t *testing.T) {
	err := &errMsg{"task already exists"}
	if !IsDedupeError(err) {
		t.Error("expected dedup error for 'task already exists'")
	}
}

func TestIsDedupeError_OtherError(t *testing.T) {
	err := errors.New("connection refused")
	if IsDedupeError(err) {
		t.Error("expected non-dedup error")
	}
}

func TestIsDedupeError_Nil(t *testing.T) {
	if IsDedupeError(nil) {
		t.Error("expected nil to not match dedup")
	}
}
