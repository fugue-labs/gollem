package core

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSchemaForPrimitives(t *testing.T) {
	tests := []struct {
		name     string
		schema   Schema
		wantType string
	}{
		{"string", SchemaFor[string](), "string"},
		{"bool", SchemaFor[bool](), "boolean"},
		{"int", SchemaFor[int](), "integer"},
		{"int8", SchemaFor[int8](), "integer"},
		{"int16", SchemaFor[int16](), "integer"},
		{"int32", SchemaFor[int32](), "integer"},
		{"int64", SchemaFor[int64](), "integer"},
		{"uint", SchemaFor[uint](), "integer"},
		{"uint8", SchemaFor[uint8](), "integer"},
		{"uint32", SchemaFor[uint32](), "integer"},
		{"uint64", SchemaFor[uint64](), "integer"},
		{"float32", SchemaFor[float32](), "number"},
		{"float64", SchemaFor[float64](), "number"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.schema["type"].(string)
			if !ok || got != tt.wantType {
				t.Errorf("SchemaFor[%s]() type = %v, want %v", tt.name, got, tt.wantType)
			}
		})
	}
}

func TestSchemaForTimeTime(t *testing.T) {
	s := SchemaFor[time.Time]()
	if s["type"] != "string" || s["format"] != "date-time" {
		t.Errorf("SchemaFor[time.Time]() = %v, want type=string format=date-time", s)
	}
}

func TestSchemaForSlice(t *testing.T) {
	s := SchemaFor[[]string]()
	if s["type"] != "array" {
		t.Fatalf("expected type=array, got %v", s["type"])
	}
	items, ok := s["items"].(Schema)
	if !ok {
		t.Fatal("items is not a Schema")
	}
	if items["type"] != "string" {
		t.Errorf("items type = %v, want string", items["type"])
	}
}

func TestSchemaForMap(t *testing.T) {
	s := SchemaFor[map[string]int]()
	if s["type"] != "object" {
		t.Fatalf("expected type=object, got %v", s["type"])
	}
	ap, ok := s["additionalProperties"].(Schema)
	if !ok {
		t.Fatal("additionalProperties is not a Schema")
	}
	if ap["type"] != "integer" {
		t.Errorf("additionalProperties type = %v, want integer", ap["type"])
	}
}

func TestSchemaForPointer(t *testing.T) {
	// Pointer to string should produce same schema as string.
	s := SchemaFor[*string]()
	if s["type"] != "string" {
		t.Errorf("SchemaFor[*string]() type = %v, want string", s["type"])
	}
}

type SimpleStruct struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestSchemaForStruct(t *testing.T) {
	s := SchemaFor[SimpleStruct]()
	if s["type"] != "object" {
		t.Fatalf("expected type=object, got %v", s["type"])
	}

	props, ok := s["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties is not a map")
	}
	if len(props) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(props))
	}

	nameSchema := props["name"].(Schema)
	if nameSchema["type"] != "string" {
		t.Errorf("name type = %v, want string", nameSchema["type"])
	}

	ageSchema := props["age"].(Schema)
	if ageSchema["type"] != "integer" {
		t.Errorf("age type = %v, want integer", ageSchema["type"])
	}

	// Both fields should be required (non-pointer, no omitempty).
	required, ok := s["required"].([]string)
	if !ok {
		t.Fatal("required is not []string")
	}
	if len(required) != 2 {
		t.Errorf("expected 2 required fields, got %d", len(required))
	}
}

type StructWithTags struct {
	Query   string  `json:"query" jsonschema:"description=Search query,required"`
	Limit   int     `json:"limit" jsonschema:"description=Max results,minimum=1,maximum=100"`
	Offset  *int    `json:"offset,omitempty" jsonschema:"description=Starting offset"`
	Format  string  `json:"format" jsonschema:"enum=json|csv|xml"`
}

func TestSchemaForStructWithTags(t *testing.T) {
	s := SchemaFor[StructWithTags]()

	props := s["properties"].(map[string]any)

	// Check query has description.
	querySchema := props["query"].(Schema)
	if querySchema["description"] != "Search query" {
		t.Errorf("query description = %v, want 'Search query'", querySchema["description"])
	}

	// Check limit has min/max.
	limitSchema := props["limit"].(Schema)
	if limitSchema["minimum"] != float64(1) {
		t.Errorf("limit minimum = %v, want 1", limitSchema["minimum"])
	}
	if limitSchema["maximum"] != float64(100) {
		t.Errorf("limit maximum = %v, want 100", limitSchema["maximum"])
	}

	// Check format has enum.
	formatSchema := props["format"].(Schema)
	enum, ok := formatSchema["enum"].([]any)
	if !ok {
		t.Fatal("format enum is not []any")
	}
	if len(enum) != 3 {
		t.Errorf("expected 3 enum values, got %d", len(enum))
	}

	// Check offset is optional (pointer type).
	required := s["required"].([]string)
	for _, r := range required {
		if r == "offset" {
			t.Error("offset should not be required (pointer type)")
		}
	}
}

type StructWithOptional struct {
	Required string `json:"required"`
	Optional string `json:"optional,omitempty"`
}

func TestSchemaOmitemptyNotRequired(t *testing.T) {
	s := SchemaFor[StructWithOptional]()
	required := s["required"].([]string)
	if len(required) != 1 || required[0] != "required" {
		t.Errorf("expected only 'required' in required list, got %v", required)
	}
}

type NestedStruct struct {
	Name    string       `json:"name"`
	Address AddressStruct `json:"address"`
}

type AddressStruct struct {
	Street string `json:"street"`
	City   string `json:"city"`
}

func TestSchemaForNestedStruct(t *testing.T) {
	s := SchemaFor[NestedStruct]()
	props := s["properties"].(map[string]any)
	addrSchema := props["address"].(Schema)
	if addrSchema["type"] != "object" {
		t.Errorf("address type = %v, want object", addrSchema["type"])
	}
	addrProps := addrSchema["properties"].(map[string]any)
	if len(addrProps) != 2 {
		t.Errorf("expected 2 address properties, got %d", len(addrProps))
	}
}

type EmbeddedBase struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type StructWithEmbedded struct {
	EmbeddedBase
	Extra string `json:"extra"`
}

func TestSchemaForEmbeddedStruct(t *testing.T) {
	s := SchemaFor[StructWithEmbedded]()
	props := s["properties"].(map[string]any)

	// Embedded fields should be flattened.
	if _, ok := props["id"]; !ok {
		t.Error("expected 'id' from embedded struct")
	}
	if _, ok := props["name"]; !ok {
		t.Error("expected 'name' from embedded struct")
	}
	if _, ok := props["extra"]; !ok {
		t.Error("expected 'extra' field")
	}
}

type SkippedField struct {
	Visible string `json:"visible"`
	Hidden  string `json:"-"`
}

func TestSchemaSkipsJsonDash(t *testing.T) {
	s := SchemaFor[SkippedField]()
	props := s["properties"].(map[string]any)
	if _, ok := props["Hidden"]; ok {
		t.Error("json:\"-\" field should be skipped")
	}
	if _, ok := props["visible"]; !ok {
		t.Error("visible field should be present")
	}
}

func TestSchemaForRawMessage(t *testing.T) {
	s := SchemaFor[json.RawMessage]()
	// Should be an empty schema (any type).
	if len(s) != 0 {
		t.Errorf("expected empty schema for json.RawMessage, got %v", s)
	}
}

func TestSchemaForByteSlice(t *testing.T) {
	s := SchemaFor[[]byte]()
	if s["type"] != "string" || s["format"] != "byte" {
		t.Errorf("expected type=string format=byte for []byte, got %v", s)
	}
}

func TestSchemaForInterface(t *testing.T) {
	s := SchemaFor[any]()
	if len(s) != 0 {
		t.Errorf("expected empty schema for any, got %v", s)
	}
}

func TestIsObjectSchema(t *testing.T) {
	tests := []struct {
		name   string
		schema Schema
		want   bool
	}{
		{"object with props", Schema{"type": "object", "properties": map[string]any{"a": Schema{}}}, true},
		{"object without props", Schema{"type": "object"}, false},
		{"string", Schema{"type": "string"}, false},
		{"empty", Schema{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsObjectSchema(tt.schema); got != tt.want {
				t.Errorf("IsObjectSchema() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Test that schema can handle recursive types without infinite loop.
type RecursiveStruct struct {
	Name     string           `json:"name"`
	Children []RecursiveStruct `json:"children"`
}

func TestSchemaForRecursiveStruct(t *testing.T) {
	// Should not hang or panic.
	s := SchemaFor[RecursiveStruct]()
	if s["type"] != "object" {
		t.Errorf("expected type=object, got %v", s["type"])
	}
}

// Custom type implementing JSONSchemaer.
type CustomType struct{}

func (CustomType) JSONSchema() Schema {
	return Schema{
		"type":        "string",
		"format":      "custom",
		"description": "A custom type",
	}
}

func TestSchemaForCustomType(t *testing.T) {
	s := SchemaFor[CustomType]()
	if s["type"] != "string" || s["format"] != "custom" {
		t.Errorf("expected custom schema, got %v", s)
	}
}
