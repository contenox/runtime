package apiframework

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUnit_ListParams_BehaviorTable(t *testing.T) {
	const defaultLimit = 100
	const validCursor = "2024-03-01T12:00:00.123456789Z"

	for _, tc := range []struct {
		name       string
		query      string
		wantCursor *time.Time
		wantLimit  int
		wantErr    bool
		wantParam  string
	}{
		{
			name:      "absent cursor and absent limit",
			query:     "",
			wantLimit: defaultLimit,
		},
		{
			name:      "empty cursor and empty limit are absent, not invalid",
			query:     "cursor=&limit=",
			wantLimit: defaultLimit,
		},
		{
			name:       "valid cursor",
			query:      "cursor=" + validCursor,
			wantCursor: mustParseCursor(t, validCursor),
			wantLimit:  defaultLimit,
		},
		{
			name:      "malformed cursor",
			query:     "cursor=garbage",
			wantErr:   true,
			wantParam: "cursor",
		},
		{
			name:      "valid limit",
			query:     "limit=7",
			wantLimit: 7,
		},
		{
			name:      "limit of 1 is the smallest accepted value",
			query:     "limit=1",
			wantLimit: 1,
		},
		{
			name:      "malformed limit",
			query:     "limit=not-a-number",
			wantErr:   true,
			wantParam: "limit",
		},
		{
			name:      "limit of zero is refused",
			query:     "limit=0",
			wantErr:   true,
			wantParam: "limit",
		},
		{
			name:      "negative limit is refused",
			query:     "limit=-5",
			wantErr:   true,
			wantParam: "limit",
		},
		{
			name:       "valid cursor and valid limit together",
			query:      "cursor=" + validCursor + "&limit=3",
			wantCursor: mustParseCursor(t, validCursor),
			wantLimit:  3,
		},
		{
			name:      "cursor is reported first when both are malformed",
			query:     "cursor=garbage&limit=nonsense",
			wantErr:   true,
			wantParam: "cursor",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/things?"+tc.query, nil)

			cursor, limit, err := ListParams(r, defaultLimit)

			if tc.wantErr {
				require.Error(t, err)
				require.True(t, errors.Is(err, ErrInvalidParameterValue),
					"error %v must wrap ErrInvalidParameterValue so it maps to a status", err)
				require.Equal(t, tc.wantParam, GetErrorParam(err))
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.wantLimit, limit)
			if tc.wantCursor == nil {
				require.Nil(t, cursor)
				return
			}
			require.NotNil(t, cursor)
			require.True(t, cursor.Equal(*tc.wantCursor), "cursor = %v, want %v", *cursor, *tc.wantCursor)
		})
	}
}

func TestUnit_ListParams_InvalidInputMapsTo400(t *testing.T) {
	for _, query := range []string{"cursor=garbage", "limit=not-a-number", "limit=0"} {
		t.Run(query, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/things?"+query, nil)
			_, _, err := ListParams(r, 100)
			require.Error(t, err)
			for _, op := range []Operation{ListOperation, GetOperation, DeleteOperation, CreateOperation} {
				require.Equal(t, http.StatusBadRequest, mapErrorToStatus(op, err),
					"operation %d must not change the status of a malformed parameter", op)
			}
		})
	}
}

func TestUnit_ListParams_ErrorBodyNamesTheParameter(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/things?cursor=garbage", nil)
	_, _, err := ListParams(r, 100)
	require.Error(t, err)

	rec := httptest.NewRecorder()
	require.NoError(t, Error(rec, r, err, ListOperation))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":{
		"message":"invalid cursor format, expected an RFC3339Nano timestamp",
		"type":"invalid_request_error",
		"param":"cursor",
		"code":"invalid_parameter_value"
	}}`, rec.Body.String())
}

func TestUnit_LimitParam_AbsentKeepsCallSiteDefault(t *testing.T) {
	for _, defaultLimit := range []int{0, -1, 25, 100} {
		r := httptest.NewRequest(http.MethodGet, "/things", nil)
		got, err := LimitParam(r, defaultLimit)
		require.NoError(t, err)
		require.Equal(t, defaultLimit, got)
	}
}

func TestUnit_LimitParam_IgnoresCursor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/things?cursor=garbage&limit=4", nil)
	got, err := LimitParam(r, 100)
	require.NoError(t, err)
	require.Equal(t, 4, got)
}

func mustParseCursor(t *testing.T, raw string) *time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	require.NoError(t, err)
	return &parsed
}
