// Package jsonstrict rejects ambiguous JSON before decoding typed requests.
package jsonstrict

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
)

func Decode(data []byte, destination any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("expected a JSON object")
	}
	if err := object(d, 0, indirect(reflect.TypeOf(destination))); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(destination); err != nil {
		return errors.New("JSON does not match the request schema")
	}
	return nil
}

func indirect(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	return t
}

func object(d *json.Decoder, depth int, schema reflect.Type) error {
	fields := map[string]reflect.Type{}
	if schema != nil && schema.Kind() == reflect.Struct {
		for i := range schema.NumField() {
			field := schema.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" {
				name = field.Name
			}
			if field.IsExported() && name != "-" {
				fields[name] = field.Type
			}
		}
	}
	seen := make(map[string]bool)
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return errors.New("invalid JSON object")
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return errors.New("duplicate or invalid JSON key")
		}
		seen[name] = true
		var child reflect.Type
		if schema != nil && schema.Kind() == reflect.Struct {
			var exists bool
			child, exists = fields[name]
			if !exists {
				return errors.New("unknown JSON field")
			}
		} else if schema != nil && schema.Kind() == reflect.Map {
			child = schema.Elem()
		}
		if err := value(d, depth+1, indirect(child)); err != nil {
			return err
		}
	}
	token, err := d.Token()
	if err != nil || token != json.Delim('}') {
		return errors.New("invalid JSON object")
	}
	return nil
}
func value(d *json.Decoder, depth int, schema reflect.Type) error {
	if depth > 64 {
		return errors.New("JSON nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil || token == nil {
		return errors.New("invalid or null JSON value")
	}
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			return object(d, depth, schema)
		case '[':
			var child reflect.Type
			if schema != nil && (schema.Kind() == reflect.Slice || schema.Kind() == reflect.Array) {
				child = indirect(schema.Elem())
			}
			for d.More() {
				if err := value(d, depth+1, child); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("invalid JSON array")
			}
		default:
			return errors.New("invalid JSON delimiter")
		}
	}
	return nil
}
