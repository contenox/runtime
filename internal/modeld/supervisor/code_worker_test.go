package supervisor_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnit_SharedOwner_CodeOperationsReuseGenerationAllocation(t *testing.T) {
	fixture := sharedOwner(t)
	client := connectOwner(t, fixture)
	var identity string
	for _, role := range []string{"primary", "autocomplete", "next-edit", "alternative"} {
		request := roleRequest(fixture, role)
		if role == "autocomplete" || role == "next-edit" {
			request.Config.Operation = role
		}
		request.Config.SessionNamespace = role + ":document"
		request.Config.Execution.RequesterID = role + ":request"
		if role == "autocomplete" {
			request.Config.Execution.Priority = "autocomplete"
		}
		session, err := client.OpenSession(t.Context(), request)
		require.NoError(t, err)
		require.NoError(t, session.Close())
		status, err := client.Status(t.Context())
		require.NoError(t, err)
		require.Len(t, status.Workers, 1)
		if identity == "" {
			identity = status.Workers[0].Instance
		}
		require.Equal(t, identity, status.Workers[0].Instance)
	}
	status, err := client.Admission(t.Context())
	require.NoError(t, err)
	require.Len(t, status.Resources.Allocations, 1)
}
