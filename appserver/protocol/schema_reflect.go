package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

var (
	protocolTimeType       = reflect.TypeOf(time.Time{})
	protocolRawMessageType = reflect.TypeOf(json.RawMessage{})
)

func schemaForDefinition(t reflect.Type, names map[reflect.Type]string) Schema {
	return schemaForType(t, make(map[reflect.Type]bool), names, t)
}

func schemaForType(t reflect.Type, visiting map[reflect.Type]bool, names map[reflect.Type]string, root reflect.Type) Schema {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t != root {
		if name, ok := names[t]; ok {
			return Schema{"$ref": "#/$defs/" + name}
		}
	}
	if t == protocolTimeType {
		return Schema{"type": "string", "format": "date-time"}
	}
	if t == protocolRawMessageType {
		return Schema{}
	}
	switch t.Kind() {
	case reflect.Bool:
		return Schema{"type": "boolean"}
	case reflect.String:
		return Schema{"type": "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return Schema{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return Schema{"type": "number"}
	case reflect.Interface:
		return Schema{}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return Schema{"type": "string", "contentEncoding": "base64"}
		}
		return Schema{"type": "array", "items": schemaForType(t.Elem(), visiting, names, root)}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return Schema{}
		}
		return Schema{"type": "object", "additionalProperties": schemaForType(t.Elem(), visiting, names, root)}
	case reflect.Struct:
		return schemaForStruct(t, visiting, names, root)
	default:
		return Schema{}
	}
}

func schemaForStruct(t reflect.Type, visiting map[reflect.Type]bool, names map[reflect.Type]string, root reflect.Type) Schema {
	if visiting[t] {
		return Schema{"type": "object"}
	}
	visiting[t] = true
	defer delete(visiting, t)
	properties := Schema{}
	required := make([]string, 0, t.NumField())
	collectSchemaFields(t, visiting, names, root, properties, &required)
	schema := Schema{
		"type":                 "object",
		"additionalProperties": false,
	}
	if len(properties) > 0 {
		schema["properties"] = properties
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func collectSchemaFields(t reflect.Type, visiting map[reflect.Type]bool, names map[reflect.Type]string, root reflect.Type, properties Schema, required *[]string) {
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, omitempty, skip := jsonFieldName(field)
		if skip {
			continue
		}
		fieldType := field.Type
		baseType := fieldType
		for baseType.Kind() == reflect.Pointer {
			baseType = baseType.Elem()
		}
		if field.Anonymous && name == "" && baseType.Kind() == reflect.Struct && baseType != protocolTimeType {
			collectSchemaFields(baseType, visiting, names, root, properties, required)
			continue
		}
		if name == "" {
			name = field.Name
		}
		fieldSchema := schemaForType(fieldType, visiting, names, root)
		if schemaTypeCanBeNil(fieldType) && len(fieldSchema) > 0 {
			fieldSchema = Schema{"anyOf": []any{fieldSchema, Schema{"type": "null"}}}
		}
		applyProtocolSchemaTag(fieldSchema, field.Tag.Get("jsonschema"))
		properties[name] = fieldSchema
		if !omitempty {
			*required = append(*required, name)
		}
	}
}

func jsonFieldName(field reflect.StructField) (string, bool, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	omitempty := false
	for _, option := range parts[1:] {
		if option == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty, false
}

func schemaTypeCanBeNil(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		return true
	default:
		return false
	}
}

func applyProtocolSchemaTag(schema Schema, tag string) {
	for _, directive := range strings.Split(tag, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(directive), "=")
		if !ok {
			continue
		}
		switch key {
		case "enum":
			values := strings.Split(value, "|")
			enum := make([]any, 0, len(values))
			for _, item := range values {
				enum = append(enum, strings.TrimSpace(item))
			}
			applySchemaConstraint(schema, "enum", enum)
		case "description":
			applySchemaConstraint(schema, "description", value)
		}
	}
}

func applySchemaConstraint(schema Schema, key string, value any) {
	if variants, ok := schema["anyOf"].([]any); ok && len(variants) > 0 {
		if first, ok := variants[0].(Schema); ok {
			first[key] = value
			return
		}
	}
	schema[key] = value
}
