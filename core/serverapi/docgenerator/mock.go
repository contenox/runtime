package docgenerator

import (
	"net/http"
	"reflect"
)

// Define a named struct for test purposes
type TestStruct struct {
	Name string `json:"name"`
}

type MockContributor struct {
	GetDocFunc func() ([]DocOperation, []DocSchema, error)
}

type FileCreateRequest struct {
	FileContent string `json:"file"`
	Name        string `json:"name"`
	ParentID    string `json:"parentid"`
}

func (m *MockContributor) GetDocumentation() ([]DocOperation, []DocSchema, error) {
	if m.GetDocFunc != nil {
		return m.GetDocFunc()
	}
	// Default implementation
	return []DocOperation{
			{
				Method:      http.MethodGet,
				Path:        "/test",
				OperationID: "getTest",
				Responses: map[int]DocSchema{
					200: {Name: "TestSchema"},
				},
			},
		}, []DocSchema{
			{
				Name:   "TestSchema",
				GoType: reflect.TypeOf(TestStruct{}),
			},
			{
				Name:   "FileCreateRequest_Multipart",
				GoType: reflect.TypeOf(FileCreateRequest{}),
			},
		}, nil
}
