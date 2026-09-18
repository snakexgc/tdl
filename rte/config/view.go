// Package config creates immutable, schema-checked component configuration.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"

	"github.com/snakexgc/tdl/interfaces/manifest"
)

type View struct {
	values  map[string]json.RawMessage
	secrets map[string]bool
}

// New accepts only declared fields, filling absent fields from schema defaults.
// Encoding values also takes ownership of caller-supplied maps and slices.
func New(schema []manifest.ConfigField, values map[string]any) (View, error) {
	v := View{values: make(map[string]json.RawMessage, len(schema)), secrets: make(map[string]bool)}
	for _, f := range schema {
		if f.Name == "" {
			return View{}, fmt.Errorf("empty config field name")
		}
		if _, exists := v.values[f.Name]; exists {
			return View{}, fmt.Errorf("duplicate field %q", f.Name)
		}
		value, exists := values[f.Name]
		if !exists {
			value = f.Default
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return View{}, fmt.Errorf("%s: %w", f.Name, err)
		}
		if err := validate(f, raw); err != nil {
			return View{}, fmt.Errorf("%s: %w", f.Name, err)
		}
		v.values[f.Name] = raw
		v.secrets[f.Name] = f.Secret
	}
	for name := range values {
		if _, ok := v.values[name]; !ok {
			return View{}, fmt.Errorf("undeclared config field %q", name)
		}
	}
	return v, nil
}

func validate(f manifest.ConfigField, raw []byte) error {
	if bytes.Equal(raw, []byte("null")) {
		return fmt.Errorf("value is required")
	}
	switch f.Type {
	case manifest.String:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if len(f.Choices) > 0 && !slices.Contains(f.Choices, value) {
			return fmt.Errorf("must be one of %s", strings.Join(f.Choices, ", "))
		}
		switch f.Format {
		case "":
		case "nonempty":
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("must not be empty")
			}
		case "url", "proxy":
			if value == "" {
				return nil
			}
			u, err := url.Parse(value)
			if err != nil || u.Hostname() == "" || strings.ContainsAny(value, "\r\n\t ") {
				return fmt.Errorf("invalid %s", f.Format)
			}
			allowed := []string{"http", "https"}
			if f.Format == "proxy" {
				allowed = append(allowed, "socks5", "socks5h")
			}
			if !slices.Contains(allowed, u.Scheme) {
				return fmt.Errorf("unsupported %s scheme", f.Format)
			}
		default:
			return fmt.Errorf("unsupported string format")
		}
		return nil
	case manifest.Bool:
		var value bool
		return json.Unmarshal(raw, &value)
	case manifest.Strings:
		var value []string
		return json.Unmarshal(raw, &value)
	case manifest.Int:
		var value int64
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if f.Min != nil && value < *f.Min {
			return fmt.Errorf("must be >= %d", *f.Min)
		}
		if f.Max != nil && value > *f.Max {
			return fmt.Errorf("must be <= %d", *f.Max)
		}
		return nil
	default:
		return fmt.Errorf("unsupported field type %q", f.Type)
	}
}

func (v View) Get(name string, target any) error {
	raw, ok := v.values[name]
	if !ok {
		return fmt.Errorf("config access denied: %q", name)
	}
	return json.Unmarshal(raw, target)
}

func (v View) Equal(other View) bool {
	if len(v.values) != len(other.values) {
		return false
	}
	for key, raw := range v.values {
		if !bytes.Equal(raw, other.values[key]) {
			return false
		}
	}
	return true
}

func Decode(schema []manifest.ConfigField, r io.Reader) (View, error) {
	var values map[string]any
	d := json.NewDecoder(r)
	d.UseNumber()
	if err := d.Decode(&values); err != nil {
		return View{}, err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return View{}, fmt.Errorf("expected one JSON object")
	}
	return New(schema, values)
}

func (v View) EqualFields(other View, names ...string) bool {
	for _, name := range names {
		if !bytes.Equal(v.values[name], other.values[name]) {
			return false
		}
	}
	return true
}
