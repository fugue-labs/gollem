package core

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Schema represents a JSON Schema document.
type Schema = map[string]any

// JSONSchemaer allows types to provide their own JSON Schema.
// If a type implements this interface, SchemaFor will use its
// schema instead of generating one via reflection.
type JSONSchemaer interface {
	JSONSchema() Schema
}

var (
	timeType        = reflect.TypeOf(time.Time{})
	rawMessageType  = reflect.TypeOf(json.RawMessage{})
	jsonSchemaerType = reflect.TypeOf((*JSONSchemaer)(nil)).Elem()
)

// SchemaFor generates a JSON Schema for Go type T using reflection.
func SchemaFor[T any]() Schema {
	t := reflect.TypeFor[T]()
	return schemaForType(t, make(map[reflect.Type]bool))
}

// schemaForType generates a JSON Schema from a reflect.Type.
// visited tracks types to prevent infinite recursion on recursive types.
func schemaForType(t reflect.Type, visited map[reflect.Type]bool) Schema {
	// Dereference pointer types.
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// Check for JSONSchemaer interface.
	if t.Implements(jsonSchemaerType) {
		v := reflect.New(t).Elem().Interface().(JSONSchemaer)
		return v.JSONSchema()
	}
	if reflect.PointerTo(t).Implements(jsonSchemaerType) {
		v := reflect.New(t).Interface().(JSONSchemaer)
		return v.JSONSchema()
	}

	// Handle special types.
	switch t {
	case timeType:
		return Schema{"type": "string", "format": "date-time"}
	case rawMessageType:
		return Schema{}
	}

	// Handle by kind.
	switch t.Kind() {
	case reflect.String:
		return Schema{"type": "string"}
	case reflect.Bool:
		return Schema{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return Schema{"type": "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return Schema{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return Schema{"type": "number"}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			// []byte → string with format
			return Schema{"type": "string", "format": "byte"}
		}
		return Schema{
			"type":  "array",
			"items": schemaForType(t.Elem(), visited),
		}
	case reflect.Array:
		return Schema{
			"type":  "array",
			"items": schemaForType(t.Elem(), visited),
		}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return Schema{}
		}
		return Schema{
			"type":                 "object",
			"additionalProperties": schemaForType(t.Elem(), visited),
		}
	case reflect.Struct:
		return schemaForStruct(t, visited)
	case reflect.Interface:
		return Schema{}
	default:
		return Schema{}
	}
}

// schemaForStruct generates an object JSON Schema from a struct type.
func schemaForStruct(t reflect.Type, visited map[reflect.Type]bool) Schema {
	// Guard against recursive types.
	if visited[t] {
		return Schema{"type": "object"}
	}
	visited[t] = true
	defer delete(visited, t)

	properties := make(map[string]any)
	var required []string

	collectFields(t, properties, &required, visited)

	schema := Schema{
		"type": "object",
	}
	if len(properties) > 0 {
		schema["properties"] = properties
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// collectFields recursively collects struct fields, handling embedded structs.
func collectFields(t reflect.Type, properties map[string]any, required *[]string, visited map[reflect.Type]bool) {
	for i := range t.NumField() {
		field := t.Field(i)

		// Skip unexported fields.
		if !field.IsExported() {
			continue
		}

		// Handle embedded structs by flattening.
		if field.Anonymous {
			ft := field.Type
			for ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && ft != timeType {
				collectFields(ft, properties, required, visited)
				continue
			}
		}

		name, fieldSchema, isRequired := schemaForField(field, visited)
		if name == "" {
			continue // json:"-"
		}
		properties[name] = fieldSchema
		if isRequired {
			*required = append(*required, name)
		}
	}
}

// schemaForField generates a schema for a struct field, reading json and jsonschema tags.
func schemaForField(f reflect.StructField, visited map[reflect.Type]bool) (name string, schema Schema, isRequired bool) {
	// Parse json tag.
	jsonTag := f.Tag.Get("json")
	if jsonTag == "-" {
		return "", nil, false
	}

	name = f.Name
	omitempty := false
	if jsonTag != "" {
		parts := strings.Split(jsonTag, ",")
		if parts[0] != "" {
			name = parts[0]
		}
		for _, opt := range parts[1:] {
			if opt == "omitempty" {
				omitempty = true
			}
		}
	}

	// Determine if field is optional (pointer type or omitempty).
	fieldType := f.Type
	isPointer := fieldType.Kind() == reflect.Ptr
	isRequired = !isPointer && !omitempty

	// Generate base schema from the field type.
	schema = schemaForType(fieldType, visited)

	// Parse jsonschema tag and apply constraints.
	jsTag := f.Tag.Get("jsonschema")
	if jsTag != "" {
		applySchemaTag(schema, jsTag, &isRequired)
	}

	return name, schema, isRequired
}

// applySchemaTag parses a jsonschema struct tag and applies its directives to the schema.
func applySchemaTag(schema Schema, tag string, isRequired *bool) {
	directives := splitTag(tag)
	for _, d := range directives {
		key, value, _ := strings.Cut(d, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch key {
		case "description":
			schema["description"] = value
		case "enum":
			vals := strings.Split(value, "|")
			enumVals := make([]any, len(vals))
			for i, v := range vals {
				enumVals[i] = strings.TrimSpace(v)
			}
			schema["enum"] = enumVals
		case "minimum":
			if f, err := strconv.ParseFloat(value, 64); err == nil {
				schema["minimum"] = f
			}
		case "maximum":
			if f, err := strconv.ParseFloat(value, 64); err == nil {
				schema["maximum"] = f
			}
		case "pattern":
			schema["pattern"] = value
		case "format":
			schema["format"] = value
		case "default":
			schema["default"] = inferValue(value, schema)
		case "required":
			*isRequired = true
		case "optional":
			*isRequired = false
		}
	}
}

// splitTag splits a jsonschema tag value by commas, respecting that values
// themselves may contain commas only within enum pipes.
func splitTag(tag string) []string {
	var result []string
	var current strings.Builder
	depth := 0
	for _, r := range tag {
		switch r {
		case '(':
			depth++
			current.WriteRune(r)
		case ')':
			depth--
			current.WriteRune(r)
		case ',':
			if depth == 0 {
				s := strings.TrimSpace(current.String())
				if s != "" {
					result = append(result, s)
				}
				current.Reset()
			} else {
				current.WriteRune(r)
			}
		default:
			current.WriteRune(r)
		}
	}
	s := strings.TrimSpace(current.String())
	if s != "" {
		result = append(result, s)
	}
	return result
}

// inferValue attempts to convert a string value to the appropriate Go type
// based on the schema type.
func inferValue(value string, schema Schema) any {
	schemaType, _ := schema["type"].(string)
	switch schemaType {
	case "integer":
		if i, err := strconv.ParseInt(value, 10, 64); err == nil {
			return i
		}
	case "number":
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	case "boolean":
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
	}
	return value
}

// IsObjectSchema returns true if the schema describes an object type.
func IsObjectSchema(s Schema) bool {
	t, ok := s["type"].(string)
	return ok && t == "object" && s["properties"] != nil
}
