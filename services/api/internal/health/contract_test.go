package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

type document struct {
	Paths      map[string]pathItem `yaml:"paths"`
	Components components          `yaml:"components"`
}

type pathItem struct {
	Get *operation `yaml:"get"`
}

type operation struct {
	OperationID string              `yaml:"operationId"`
	Responses   map[string]response `yaml:"responses"`
}

type response struct {
	Content map[string]mediaType `yaml:"content"`
}

type mediaType struct {
	Schema schemaRef `yaml:"schema"`
}

type schemaRef struct {
	Ref string `yaml:"$ref"`
}

type components struct {
	Schemas map[string]schemaObject `yaml:"schemas"`
}

type schemaObject struct {
	AdditionalProperties *bool               `yaml:"additionalProperties"`
	Required             []string            `yaml:"required"`
	Properties           map[string]property `yaml:"properties"`
}

type property struct {
	Const string `yaml:"const"`
}

func TestHealthMatchesOpenAPI(t *testing.T) {
	contract := loadContract(t)
	healthPath, ok := contract.Paths["/v1/health"]
	if !ok || healthPath.Get == nil {
		t.Fatal("missing GET /v1/health")
	}
	if healthPath.Get.OperationID != "getHealth" {
		t.Fatalf("operationId = %q", healthPath.Get.OperationID)
	}
	okResponse, ok := healthPath.Get.Responses["200"]
	if !ok {
		t.Fatal("missing 200 response")
	}
	media, ok := okResponse.Content["application/json"]
	if !ok {
		t.Fatal("missing application/json")
	}
	if media.Schema.Ref != "#/components/schemas/Health" {
		t.Fatalf("schema ref = %q", media.Schema.Ref)
	}
	schema, ok := contract.Components.Schemas["Health"]
	if !ok {
		t.Fatal("missing Health schema")
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatal("Health must set additionalProperties to false")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "status" {
		t.Fatalf("required = %v", schema.Required)
	}
	statusField, ok := schema.Properties["status"]
	if !ok || statusField.Const != OK().Status {
		t.Fatalf("status const = %+v", statusField)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	recorder := httptest.NewRecorder()
	NewMux().ServeHTTP(recorder, request)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw) != len(schema.Properties) {
		t.Fatalf("field count = %d", len(raw))
	}
	for name := range schema.Properties {
		if _, found := raw[name]; !found {
			t.Fatalf("missing field %s", name)
		}
	}
}

func loadContract(t *testing.T) document {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "contracts", "openapi", "v1.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	var contract document
	if err := yaml.Unmarshal(body, &contract); err != nil {
		t.Fatalf("parse contract: %v", err)
	}
	return contract
}
