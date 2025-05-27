package docgenerator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3gen"
)

type DocGenerator struct {
	swagger             *openapi3.T
	processedSchemaRefs map[reflect.Type]*openapi3.SchemaRef
}

// DocContributor is the interface each API module implements to provide its documentation.
type DocContributor interface {
	GetDocumentation() ([]DocOperation, []DocSchema, error)
}

// ParamLocation indicates where a parameter is found.
type ParamLocation string

const (
	ParamInPath   ParamLocation = "path"
	ParamInQuery  ParamLocation = "query"
	ParamInHeader ParamLocation = "header"
	ParamInCookie ParamLocation = "cookie"
)

// DocParameter defines a single parameter.
type DocParameter struct {
	Name        string
	In          ParamLocation
	Type        reflect.Type // e.g., reflect.TypeOf("") for string, reflect.TypeOf(0) for int
	Required    bool
	Description string
	Example     interface{}
}

// DocOperation describes a single API operation (e.g., GET /files/{id}).
type DocOperation struct {
	Method      string // e.g., http.MethodGet
	Path        string // e.g., "/files/{id}"
	OperationID string // Unique identifier for the operation
	Summary     string
	Description string
	Tags        []string
	Parameters  []DocParameter
	RequestBody DocSchema         // Request body schema (nil if no body)
	Responses   map[int]DocSchema // Map HTTP status code to response schema
	// You might add specific error responses here, or have a common way to register them
	// e.g., CommonErrors []int // 400, 404, 500
}

type DocSchema struct {
	Name        string       // Unique name for the schema in components (e.g., "FileResponse")
	GoType      reflect.Type // The Go struct type (e.g., reflect.TypeOf(filesapi.FileResponse{}))
	Description string
	Example     interface{} // Optional: An example struct instance for better examples in docs
}

func NewDocGenerator(title, version string) (*DocGenerator, error) {
	doc := &DocGenerator{
		swagger: &openapi3.T{
			OpenAPI: "3.0.0",
			Info:    &openapi3.Info{Title: title, Version: version},
			Paths:   openapi3.NewPaths(),
			Components: &openapi3.Components{
				Schemas:   make(openapi3.Schemas),
				Responses: openapi3.ResponseBodies{},
			},
		},
		processedSchemaRefs: make(map[reflect.Type]*openapi3.SchemaRef),
	}

	err := doc.AddCommonSchema(DocSchema{
		Name: "StandardErrorResponse",
		GoType: reflect.TypeOf(struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		}{}),
		Description: "Generic error format used in error responses",
		Example: map[string]any{
			"message": "Not Found",
			"code":    404,
		},
	})

	return doc, err
}

func (dg *DocGenerator) AddCommonSchema(docSchema DocSchema) error {
	if _, exists := dg.swagger.Components.Schemas[docSchema.Name]; exists {
		return fmt.Errorf("common schema '%s' already exists", docSchema.Name)
	}
	schemaRef, err := openapi3gen.NewSchemaRefForValue(reflect.New(docSchema.GoType).Interface(), dg.swagger.Components.Schemas)
	if err != nil {
		return fmt.Errorf("failed to generate schema for %s: %w", docSchema.Name, err)
	}
	if docSchema.Example != nil {
		schemaRef.Value.Example = docSchema.Example
	}
	dg.swagger.Components.Schemas[docSchema.Name] = schemaRef
	dg.processedSchemaRefs[docSchema.GoType] = schemaRef
	return nil
}

// AddContributor processes a DocContributor to add its operations and schemas.
func (dg *DocGenerator) AddContributor(contributor DocContributor) error {
	operations, schemas, err := contributor.GetDocumentation()
	if err != nil {
		return fmt.Errorf("failed to get documentation from contributor: %w", err)
	}

	// First, add all schemas provided by this contributor
	for _, docSchema := range schemas {
		if _, exists := dg.swagger.Components.Schemas[docSchema.Name]; exists {
			// Handle potential naming conflicts across modules or re-use existing
			// For simplicity here, we assume unique names or that duplicates are identical
			continue
		}
		schemaRef, err := openapi3gen.NewSchemaRefForValue(reflect.New(docSchema.GoType).Interface(), dg.swagger.Components.Schemas)
		if err != nil {
			return fmt.Errorf("failed to generate schema for %s: %w", docSchema.Name, err)
		}
		if docSchema.Example != nil {
			schemaRef.Value.Example = docSchema.Example
		}
		dg.swagger.Components.Schemas[docSchema.Name] = schemaRef
		dg.processedSchemaRefs[docSchema.GoType] = schemaRef
	}

	// Now process operations
	for _, docOp := range operations {
		op := &openapi3.Operation{
			OperationID: docOp.OperationID,
			Summary:     docOp.Summary,
			Description: docOp.Description,
			Tags:        docOp.Tags,
			Responses:   &openapi3.Responses{},
		}

		// Convert DocParameters to openapi3.Parameters
		for _, p := range docOp.Parameters {
			paramSchema := openapi3.NewSchema()
			if p.Type != nil {
				paramSchema.Type = &openapi3.Types{}
				*paramSchema.Type = append(*paramSchema.Type, convertGoTypeToOpenAPIType(p.Type))
				// Helper function needed
				if p.Example != nil {
					paramSchema.Example = p.Example
				}
			}
			param := &openapi3.Parameter{
				Name:        p.Name,
				In:          string(p.In),
				Required:    p.Required,
				Description: p.Description,
				Schema: &openapi3.SchemaRef{
					Value: paramSchema,
				},
			}

			op.Parameters = append(op.Parameters, &openapi3.ParameterRef{Value: param})
		}

		// Convert RequestBody
		if docOp.RequestBody.Name != "" { // Check if a request body is defined
			requestBodyRef, err := dg.getSchemaRef(docOp.RequestBody)
			if err != nil {
				return fmt.Errorf("failed to get request body ref for %s %s: %w", docOp.Method, docOp.Path, err)
			}
			op.RequestBody = &openapi3.RequestBodyRef{
				Value: openapi3.NewRequestBody().
					WithDescription(docOp.RequestBody.Description).
					WithRequired(true). // Or infer from DocSchema field
					WithJSONSchemaRef(requestBodyRef),
			}
			// Special handling for multipart/form-data if you have it
			if docOp.RequestBody.Name == "FileCreateRequest_Multipart" {
				fileSchema := openapi3.NewSchema()
				fileSchema.Description = "The file content"
				fileSchema.Format = "binary"
				fileSchema.Title = "File"

				nameSchema := openapi3.NewStringSchema()
				nameSchema.Description = "Optional new name for the file"

				parentidSchema := openapi3.NewStringSchema()
				parentidSchema.Description = "Optional parent folder ID"

				multipartSchema := openapi3.NewObjectSchema().
					WithProperty("file", fileSchema). // fileSchema is *Schema
					WithProperty("name", nameSchema).
					WithProperty("parentid", parentidSchema)

				mediaType := &openapi3.MediaType{
					Schema: &openapi3.SchemaRef{Value: multipartSchema},
				}

				op.RequestBody = &openapi3.RequestBodyRef{
					Value: openapi3.NewRequestBody().
						WithDescription("File upload and metadata").
						WithRequired(true).
						WithContent(openapi3.Content{
							"multipart/form-data": mediaType,
						}),
				}
			}

			// Convert Responses
			for statusCode, docSchema := range docOp.Responses {
				responseRef, err := dg.getSchemaRef(docSchema)
				if err != nil {
					return fmt.Errorf("failed to get response schema ref: %w", err)
				}
				description := docSchema.Description
				if description == "" {
					description = fmt.Sprintf("Response for %s %s", docOp.Method, docOp.Path)
				}
				op.Responses.Set(fmt.Sprintf("%d", statusCode), &openapi3.ResponseRef{
					Value: openapi3.NewResponse().
						WithDescription(description).
						WithJSONSchemaRef(responseRef),
				})
			}

		}

		// Convert Responses
		for statusCode, docSchema := range docOp.Responses {
			responseRef, err := dg.getSchemaRef(docSchema)
			if err != nil {
				return fmt.Errorf("failed to get response schema ref for %s %s status %d: %w", docOp.Method, docOp.Path, statusCode, err)
			}

			// Set a default description if not provided in DocSchema
			description := fmt.Sprintf("Response for %s %s", docOp.Method, docOp.Path)
			if docSchema.Description != "" {
				description = docSchema.Description
			}
			op.Responses.Set(fmt.Sprintf("%d", statusCode), &openapi3.ResponseRef{
				Value: openapi3.NewResponse().
					WithDescription(description).
					WithJSONSchemaRef(responseRef),
			})
		}

		// Add operation to the global Paths map
		pathItem := dg.swagger.Paths.Find(docOp.Path)
		if pathItem == nil {
			pathItem = &openapi3.PathItem{}
			dg.swagger.Paths.Set(docOp.Path, pathItem)
		}

		switch docOp.Method {
		case http.MethodGet:
			pathItem.Get = op
		case http.MethodPost:
			pathItem.Post = op
		case http.MethodPut:
			pathItem.Put = op
		case http.MethodDelete:
			pathItem.Delete = op
		case http.MethodPatch:
			pathItem.Patch = op
		case http.MethodHead:
			pathItem.Head = op
		case http.MethodOptions:
			pathItem.Options = op
		case http.MethodTrace:
			pathItem.Trace = op
		}
	}
	return nil
}

// getSchemaRef retrieves a schema reference from components or creates it if needed.
func (dg *DocGenerator) getSchemaRef(docSchema DocSchema) (*openapi3.SchemaRef, error) {
	if docSchema.Name == "" {
		return nil, fmt.Errorf("DocSchema must have a Name for reference")
	}

	if _, ok := dg.swagger.Components.Schemas[docSchema.Name]; ok {
		return openapi3.NewSchemaRef(fmt.Sprintf("#/components/schemas/%s", docSchema.Name), nil), nil
	}

	// If schema not found, try to generate it and add to components
	if docSchema.GoType == nil {
		return nil, fmt.Errorf("schema '%s' not found in components and GoType is nil", docSchema.Name)
	}

	schemaRef, err := openapi3gen.NewSchemaRefForValue(reflect.New(docSchema.GoType).Interface(), dg.swagger.Components.Schemas)
	if err != nil {
		return nil, fmt.Errorf("failed to generate schema for %s: %w", docSchema.Name, err)
	}
	if docSchema.Example != nil {
		schemaRef.Value.Example = docSchema.Example
	}
	dg.swagger.Components.Schemas[docSchema.Name] = schemaRef
	dg.processedSchemaRefs[docSchema.GoType] = schemaRef

	return openapi3.NewSchemaRef(fmt.Sprintf("#/components/schemas/%s", docSchema.Name), nil), nil
}

func convertGoTypeToOpenAPIType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Bool:
		return "boolean"
	case reflect.Array, reflect.Slice:
		return "array"
	case reflect.Map, reflect.Struct:
		// For complex types, we expect them to be registered as schemas in components.
		// This function only returns primitive types. NO WE DON'T!
		return "object"
	default:
		return "string"
	}
}

// GetSpec returns the marshaled OpenAPI spec.
func (dg *DocGenerator) GetSpec() ([]byte, error) {
	if commonErrorSchemaRef, ok := dg.swagger.Components.Schemas["StandardErrorResponse"]; ok {
		if _, ok := dg.swagger.Components.Responses["StandardErrorResponse"]; !ok {
			dg.swagger.Components.Responses["StandardErrorResponse"] = &openapi3.ResponseRef{
				Value: openapi3.NewResponse().WithDescription("Standard Error").WithJSONSchemaRef(commonErrorSchemaRef),
			}
		}
	}

	if err := dg.swagger.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("generated OpenAPI spec is invalid: %w", err)
	}
	return json.MarshalIndent(dg.swagger, "", "  ")
}
