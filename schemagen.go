package main

import (
	"reflect"
	"strings"
)

// jsonSchemaFor builds a JSON Schema for a Go type by reflection. It mirrors how
// encoding/json serializes the type (json tags, embedded structs), so a tool's
// declared output schema is generated from the very struct the handler returns
// and cannot drift from it.
func jsonSchemaFor(t reflect.Type) map[string]any {
	switch t.Kind() {
	case reflect.Pointer:
		return jsonSchemaFor(t.Elem())
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 { // []byte serializes as a string
			return map[string]any{"type": "string"}
		}
		return map[string]any{"type": "array", "items": jsonSchemaFor(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": jsonSchemaFor(t.Elem())}
	case reflect.Struct:
		props := map[string]any{}
		for i := range t.NumField() {
			f := t.Field(i)
			if f.PkgPath != "" { // unexported
				continue
			}
			name, ok := jsonFieldName(f)
			if !ok {
				continue
			}
			props[name] = jsonSchemaFor(f.Type)
		}
		return map[string]any{"type": "object", "properties": props}
	case reflect.Interface:
		return map[string]any{} // `any` — no constraint
	default:
		return map[string]any{}
	}
}

// jsonFieldName returns the serialized name of a struct field and whether it is
// serialized at all (false for json:"-").
func jsonFieldName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	name := f.Name
	if tag != "" {
		if first := strings.Split(tag, ",")[0]; first != "" {
			name = first
		}
	}
	return name, true
}

// outputSchemaFor returns the JSON Schema for a command's result. Commands with
// no OutputType (dynamic shapes like a raw Autotask object) get a loose object
// schema; commands with a distinct dry-run shape get a oneOf of both.
func outputSchemaFor(c command) map[string]any {
	if c.OutputType == nil {
		return map[string]any{"type": "object"}
	}
	live := jsonSchemaFor(reflect.TypeOf(c.OutputType))
	if c.DryRunType == nil {
		return live
	}
	return map[string]any{"oneOf": []map[string]any{live, jsonSchemaFor(reflect.TypeOf(c.DryRunType))}}
}
