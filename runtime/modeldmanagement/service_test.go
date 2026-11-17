package modeldmanagement_test

import (
	"context"
	"testing"

	"github.com/contenox/runtime/runtime/modeldmanagement"
	"github.com/contenox/runtime/runtime/modeldprobe"
)

func TestUnit_ModeldManagement_StatusUsesConfiguredDataRoot(t *testing.T) {
	service, err := modeldmanagement.New(t.TempDir(), "", "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	result, err := modeldmanagement.Execute(context.Background(), service, modeldmanagement.Request{Action: modeldmanagement.ActionStatus})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == nil || result.Status.State != modeldprobe.StateNotInstalled {
		t.Fatalf("unexpected status: %+v", result.Status)
	}
}

func TestUnit_ModeldManagement_RejectsMissingClientVersion(t *testing.T) {
	if _, err := modeldmanagement.New(t.TempDir(), "", ""); err == nil {
		t.Fatal("expected missing client version error")
	}
}
