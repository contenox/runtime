package docgenerator_test

import (
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"reflect"
	"testing"

	"github.com/contenox/contenox/core/serverapi/docgenerator"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestNewDocGenerator(t *testing.T) {
	title := "Test API"
	version := "1.0.0"
	dg, err := docgenerator.NewDocGenerator(title, version)
	if err != nil {
		t.Fatalf("Failed to create DocGenerator: %v", err)
	}

	spec, err := dg.GetSpec()
	if err != nil {
		t.Fatal(err)
	}

	var swagger openapi3.T
	if err := json.Unmarshal(spec, &swagger); err != nil {
		t.Fatal(err)
	}

	if swagger.Info.Title != title {
		t.Errorf("Expected title %s, got %s", title, swagger.Info.Title)
	}
	if swagger.Info.Version != version {
		t.Errorf("Expected version %s, got %s", version, swagger.Info.Version)
	}
	if _, ok := swagger.Components.Schemas["StandardErrorResponse"]; !ok {
		t.Error("StandardErrorResponse schema not found in components")
	}
	t.Log(string(spec))

	if err := swagger.Validate(context.Background()); err != nil {
		t.Fatalf("Invalid OpenAPI spec: %v", err)
	}
}

func TestAddContributor(t *testing.T) {
	dg, _ := docgenerator.NewDocGenerator("Test", "1.0")
	err := dg.AddContributor(&docgenerator.MockContributor{})
	if err != nil {
		t.Fatalf("AddContributor failed: %v", err)
	}

	spec, err := dg.GetSpec()
	if err != nil {
		t.Fatal(err)
	}

	var swagger openapi3.T
	if err := json.Unmarshal(spec, &swagger); err != nil {
		t.Fatal(err)
	}

	if _, ok := swagger.Components.Schemas["TestSchema"]; !ok {
		t.Error("TestSchema not found in components")
	}

	pathItem := swagger.Paths.Find("/test")
	if pathItem == nil || pathItem.Get == nil {
		t.Error("GET operation not added correctly")
	}
	t.Log(string(spec))

	if err := swagger.Validate(context.Background()); err != nil {
		t.Fatalf("Invalid OpenAPI spec: %v", err)
	}
}

type TestStruct struct {
	Name string `json:"name"`
}

type FileCreateRequest struct {
	FileContent string `json:"file"`
	Name        string `json:"name"`
	ParentID    string `json:"parentid"`
}

func TestParametersConversion(t *testing.T) {
	dg, _ := docgenerator.NewDocGenerator("Test", "1.0")
	contributor := &docgenerator.MockContributor{
		GetDocFunc: func() ([]docgenerator.DocOperation, []docgenerator.DocSchema, error) {
			op := docgenerator.DocOperation{
				Method:      http.MethodGet,
				Path:        "/test/{id}",
				OperationID: "getTest",
				Parameters: []docgenerator.DocParameter{
					{Name: "id", In: docgenerator.ParamInPath, Required: true, Type: reflect.TypeOf("")},
					{Name: "filter", In: docgenerator.ParamInQuery, Type: reflect.TypeOf("")},
				},
				Responses: map[int]docgenerator.DocSchema{200: {Name: "TestSchema"}},
			}
			return []docgenerator.DocOperation{op}, []docgenerator.DocSchema{
				{
					Name:   "TestSchema",
					GoType: reflect.TypeOf(TestStruct{}),
				},
			}, nil
		},
	}

	err := dg.AddContributor(contributor)
	if err != nil {
		t.Fatal(err)
	}

	spec, err := dg.GetSpec()
	if err != nil {
		t.Fatal(err)
	}

	var swagger openapi3.T
	if err := json.Unmarshal(spec, &swagger); err != nil {
		t.Fatal(err)
	}

	pathItem := swagger.Paths.Find("/test/{id}")
	params := pathItem.Get.Parameters
	if len(params) != 2 {
		t.Fatalf("Expected 2 parameters, got %d", len(params))
	}

	paramMap := make(map[string]*openapi3.Parameter)
	for _, p := range params {
		paramMap[p.Value.Name] = p.Value
	}

	if p, ok := paramMap["id"]; !ok || p.In != "path" || !p.Required {
		t.Error("Path parameter incorrect")
	}
	if p, ok := paramMap["filter"]; !ok || p.In != "query" {
		t.Error("Query parameter incorrect")
	}
	t.Log(string(spec))

	if err := swagger.Validate(context.Background()); err != nil {
		t.Fatalf("Invalid OpenAPI spec: %v", err)
	}
}

type FileCreateRequestMultipart struct {
	File     multipart.FileHeader `json:"file"`
	Name     string               `json:"name,omitempty"`
	ParentID string               `json:"parentid,omitempty"`
}

func TestMultipartRequestBody(t *testing.T) {
	dg, _ := docgenerator.NewDocGenerator("Test", "1.0")
	contributor := &docgenerator.MockContributor{
		GetDocFunc: func() ([]docgenerator.DocOperation, []docgenerator.DocSchema, error) {
			return []docgenerator.DocOperation{
					{
						Method: http.MethodPost,
						Path:   "/upload",
						RequestBody: docgenerator.DocSchema{
							Name:        "FileCreateRequest_Multipart",
							ContentType: "multipart/form-data",
						},
						Responses: map[int]docgenerator.DocSchema{200: {Name: "TestSchema"}},
					},
				}, []docgenerator.DocSchema{
					{
						Name:        "FileCreateRequest_Multipart",
						GoType:      reflect.TypeOf(FileCreateRequestMultipart{}),
						Description: "Upload file and optional metadata",
					},
					{
						Name:   "TestSchema",
						GoType: reflect.TypeOf(TestStruct{}),
					},
				}, nil
		},
	}

	err := dg.AddContributor(contributor)
	if err != nil {
		t.Fatal(err)
	}

	spec, err := dg.GetSpec()
	if err != nil {
		t.Fatal(err)
	}

	var swagger openapi3.T
	if err := json.Unmarshal(spec, &swagger); err != nil {
		t.Fatal(err)
	}

	pathItem := swagger.Paths.Find("/upload")
	mediaType := pathItem.Post.RequestBody.Value.Content["multipart/form-data"]
	t.Log(string(spec))

	if mediaType == nil {
		t.Fatal("multipart/form-data not found")
	}
	if mediaType.Schema.Value.Properties["file"] == nil {
		t.Error("File field missing in multipart schema")
	}
}

func TestDuplicateSchema(t *testing.T) {
	dg, _ := docgenerator.NewDocGenerator("Test", "1.0")
	mock := &docgenerator.MockContributor{}
	err := dg.AddContributor(mock)
	if err != nil {
		t.Fatal(err)
	}
	err = dg.AddContributor(mock)
	if err != nil {
		t.Fatal(err)
	}

	spec, err := dg.GetSpec()
	if err != nil {
		t.Fatal(err)
	}

	var swagger openapi3.T
	if err := swagger.UnmarshalJSON(spec); err != nil {
		t.Fatal(err)
	}

	count := 0
	for name := range swagger.Components.Schemas {
		if name == "TestSchema" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Duplicate schema added: found %d instances", count)
	}
}

func TestGetSpec(t *testing.T) {
	dg, _ := docgenerator.NewDocGenerator("Test", "1.0")
	dg.AddContributor(&docgenerator.MockContributor{})

	spec, err := dg.GetSpec()
	if err != nil {
		t.Fatalf("GetSpec failed: %v", err)
	}

	var specObj openapi3.T
	if err := specObj.UnmarshalJSON(spec); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}
	t.Log(string(spec))
	if err := specObj.Validate(context.Background()); err != nil {
		t.Fatalf("Invalid OpenAPI spec: %v", err)
	}
}

func TestInvalidSchema(t *testing.T) {
	dg, _ := docgenerator.NewDocGenerator("Test", "1.0")
	contributor := &docgenerator.MockContributor{
		GetDocFunc: func() ([]docgenerator.DocOperation, []docgenerator.DocSchema, error) {
			return nil, []docgenerator.DocSchema{
				{
					Name:   "Invalid",
					GoType: reflect.TypeOf(func() {}),
				},
			}, nil
		},
	}

	err := dg.AddContributor(contributor)
	if err == nil {
		t.Error("Expected error for invalid schema, got nil")
	}
}
