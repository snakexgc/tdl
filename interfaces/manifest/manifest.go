// Package manifest describes components independently of their implementations.
package manifest

import "reflect"

type FieldType string

const (
	String  FieldType = "string"
	Int     FieldType = "int"
	Bool    FieldType = "bool"
	Strings FieldType = "strings"
)

type ConfigField struct {
	Name    string    `json:"name"`
	Title   string    `json:"title"`
	Type    FieldType `json:"type"`
	Default any       `json:"default"`
	Min     *int64    `json:"min,omitempty"`
	Max     *int64    `json:"max,omitempty"`
	Secret  bool      `json:"secret,omitempty"`
}

// Port versions use a compatible major version and a minimum minor version.
// Type must be the Go interface type, not the concrete provider type.
type Port struct {
	Name  string
	Major int
	Minor int
	Type  reflect.Type
}

func PortOf[T any](name string, major, minor int) Port {
	return Port{Name: name, Major: major, Minor: minor, Type: reflect.TypeFor[T]()}
}

type Require struct {
	Port
	Optional bool
}

type Manifest struct {
	ID         string
	Title      string
	Provides   []Port
	Requires   []Require
	Config     []ConfigField
	Publishes  []string
	Subscribes []string
	Pages      []Page
}

// Page declares a same-origin feature entry owned by a component.
type Page struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}
