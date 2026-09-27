package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Error never includes scalar values from YAML. Path and coordinates locate the
// problem while keeping credentials out of diagnostics and logs.
type Error struct {
	Path    string
	Line    int
	Column  int
	Message string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("invalid_config: %s (line %d, column %d): %s", e.Path, e.Line, e.Column, e.Message)
	}
	return fmt.Sprintf("invalid_config: %s: %s", e.Path, e.Message)
}

type validator struct{ nodes map[string]*yaml.Node }

func (v *validator) fail(path, message string) error {
	e := &Error{Path: path, Message: message}
	if node := v.nodes[path]; node != nil {
		e.Line, e.Column = node.Line, node.Column
	}
	return e
}

// Load resolves the configuration filename, bounds input, then validates it.
// No project path is accessed and no environment variable values are expanded.
func Load(path string) (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, &Error{Path: "$", Message: "cannot determine home directory"}
	}
	path, err = resolvePath(path, ".", home)
	if err != nil {
		return nil, &Error{Path: "$", Message: "invalid configuration path"}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, &Error{Path: "$", Message: "cannot open configuration file"}
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return nil, &Error{Path: "$", Message: "cannot read configuration file"}
	}
	return Parse(data, filepath.Dir(path), home)
}

// Parse returns an independently owned, normalized configuration. The caller
// must treat it as immutable after publication. baseDir/home must be absolute.
func Parse(data []byte, baseDir, home string) (*Config, error) {
	v := &validator{nodes: map[string]*yaml.Node{}}
	if len(data) > MaxConfigBytes {
		return nil, v.fail("$", "configuration exceeds 1 MiB")
	}
	if !filepath.IsAbs(baseDir) || !filepath.IsAbs(home) {
		return nil, v.fail("$", "base and home directories must be absolute")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	if err := decoder.Decode(&root); err != nil {
		return nil, syntaxError(err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, v.fail("$", "exactly one YAML document is required")
	}
	if len(root.Content) != 1 {
		return nil, v.fail("$", "configuration must be a mapping")
	}
	if err := v.shape(root.Content[0], reflect.TypeFor[Config](), "$", 0); err != nil {
		return nil, err
	}
	c := defaults()
	if err := root.Decode(&c); err != nil {
		return nil, v.fail("$", "value cannot be decoded into the configuration schema")
	}
	if err := v.normalize(&c, baseDir, home); err != nil {
		return nil, err
	}
	return &c, nil
}

var yamlLine = regexp.MustCompile(`line ([0-9]+)`)

func syntaxError(err error) error {
	e := &Error{Path: "$", Message: "invalid YAML syntax"}
	if match := yamlLine.FindStringSubmatch(err.Error()); len(match) == 2 {
		e.Line, _ = strconv.Atoi(match[1])
	}
	return e
}

// shape enforces exact scalar types before decoding: the YAML library otherwise
// coerces numbers to strings and legacy yes/no values to booleans. It also rejects
// aliases/merge keys and unknown fields without including raw values in errors.
func (v *validator) shape(n *yaml.Node, t reflect.Type, path string, depth int) error {
	v.nodes[path] = n
	if depth > 64 {
		return v.fail(path, "maximum nesting depth exceeded")
	}
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return v.fail(path, "YAML aliases and anchors are unsupported")
	}
	if n.Tag == "!!null" {
		return v.fail(path, "null values are unsupported; omit optional fields")
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Interface {
		switch n.Kind {
		case yaml.MappingNode:
			t = reflect.TypeFor[map[string]any]()
		case yaml.SequenceNode:
			t = reflect.TypeFor[[]any]()
		case yaml.ScalarNode:
			if n.Tag == "!!str" || n.Tag == "!!int" || n.Tag == "!!float" || n.Tag == "!!bool" {
				return nil
			}
			return v.fail(path, "expected a JSON-compatible scalar")
		default:
			return v.fail(path, "unsupported YAML value")
		}
	}
	switch t.Kind() {
	case reflect.Struct, reflect.Map:
		if n.Kind != yaml.MappingNode || n.Tag != "!!map" {
			return v.fail(path, "expected a mapping")
		}
		fields := map[string]reflect.Type{}
		if t.Kind() == reflect.Struct {
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				fields[strings.Split(f.Tag.Get("yaml"), ",")[0]] = f.Type
			}
		}
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			key, value := n.Content[i], n.Content[i+1]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Anchor != "" || key.Value == "<<" {
				return v.fail(path, "mapping keys must be strings; YAML merges are unsupported")
			}
			child := key.Value
			if path != "$" {
				child = path + "." + child
			}
			v.nodes[child] = key
			if seen[key.Value] {
				return v.fail(child, "duplicate key")
			}
			seen[key.Value] = true
			var next reflect.Type
			if t.Kind() == reflect.Struct {
				var found bool
				next, found = fields[key.Value]
				if !found {
					return v.fail(child, "unknown field")
				}
			} else {
				next = t.Elem()
			}
			if err := v.shape(value, next, child, depth+1); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if n.Kind != yaml.SequenceNode || n.Tag != "!!seq" {
			return v.fail(path, "expected a sequence")
		}
		for i, item := range n.Content {
			if err := v.shape(item, t.Elem(), fmt.Sprintf("%s[%d]", path, i), depth+1); err != nil {
				return err
			}
		}
	case reflect.String:
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
			return v.fail(path, "expected a string")
		}
	case reflect.Bool:
		if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" {
			return v.fail(path, "expected true or false")
		}
	case reflect.Int, reflect.Int64:
		if n.Kind != yaml.ScalarNode || n.Tag != "!!int" {
			return v.fail(path, "expected an integer")
		}
	default:
		return v.fail(path, "unsupported schema type")
	}
	return nil
}

func resolvePath(path, baseDir, home string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("empty or invalid path")
	}
	if path == "~" {
		path = home
	} else if strings.HasPrefix(path, "~/") {
		path = filepath.Join(home, path[2:])
	} else if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("named-user expansion is unsupported")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	return filepath.Abs(path)
}
