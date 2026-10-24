package apiframework

import (
	"net/http"
	"strconv"
	"time"
)

func GetPathParam(r *http.Request, name string, description string) string {
	return r.PathValue(name)
}

func GetQueryParam(r *http.Request, name, defaultValue, description string) string {
	val := r.URL.Query().Get(name)
	if val == "" {
		return defaultValue
	}
	return val
}

const (
	CursorParamDescription = "An optional RFC3339Nano timestamp to fetch the next page of results."
	LimitParamDescription  = "The maximum number of items to return per page."
)

func ListParams(r *http.Request, defaultLimit int) (*time.Time, int, error) {
	cursor, err := CursorParam(r)
	if err != nil {
		return nil, 0, err
	}
	limit, err := LimitParam(r, defaultLimit)
	if err != nil {
		return nil, 0, err
	}
	return cursor, limit, nil
}

func CursorParam(r *http.Request) (*time.Time, error) {
	raw := GetQueryParam(r, "cursor", "", CursorParamDescription)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, InvalidParameterValue("cursor", "invalid cursor format, expected an RFC3339Nano timestamp")
	}
	return &t, nil
}

func LimitParam(r *http.Request, defaultLimit int) (int, error) {
	raw := GetQueryParam(r, "limit", "", LimitParamDescription)
	if raw == "" {
		return defaultLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, InvalidParameterValue("limit", "invalid limit format, expected an integer")
	}
	if limit < 1 {
		return 0, InvalidParameterValue("limit", "limit must be greater than zero")
	}
	return limit, nil
}
