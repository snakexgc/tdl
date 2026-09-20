// Package manifest describes components independently of their implementations.
package manifest

import (
	"reflect"

	"github.com/snakexgc/tdl/interfaces/types"
)

type FieldType string

const (
	String  FieldType = "string"
	Int     FieldType = "int"
	Bool    FieldType = "bool"
	Strings FieldType = "strings"
	Objects FieldType = "objects"
)

type ConfigField struct {
	ReplacedBy      string    `json:"replaced_by,omitempty"`
	SettingsTab     string    `json:"settings_tab,omitempty"`
	SettingsSection string    `json:"settings_section,omitempty"`
	SettingsOrder   int       `json:"settings_order,omitempty"`
	Advanced        bool      `json:"advanced,omitempty"`
	Help            string    `json:"help,omitempty"`
	Editor          string    `json:"editor,omitempty"`
	Choices         []string  `json:"choices,omitempty"`
	Format          string    `json:"format,omitempty"`
	RestartRequired bool      `json:"restart_required,omitempty"`
	Name            string    `json:"name"`
	Title           string    `json:"title"`
	Type            FieldType `json:"type"`
	Default         any       `json:"default"`
	Min             *int64    `json:"min,omitempty"`
	Max             *int64    `json:"max,omitempty"`
	Secret          bool      `json:"secret,omitempty"`
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
	Feature    Feature
	Commands   []types.ConsoleCommand
	ID         string
	Title      string
	Provides   []Port
	Requires   []Require
	Config     []ConfigField
	Publishes  []string
	Subscribes []string
	Pages      []Page
}

// Feature groups component status below a user-facing capability in the HMI.
type Feature struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Order       int    `json:"order,omitempty"`
	SettingsURL string `json:"settings_url,omitempty"`
}

// Page declares a same-origin feature entry owned by a component.
type Page struct {
	NavHidden   bool     `json:"nav_hidden,omitempty"`
	KeepVisible bool     `json:"keep_visible,omitempty"`
	SettingsURL string   `json:"settings_url,omitempty"`
	RedirectTo  string   `json:"redirect_to,omitempty"`
	Settings    []string `json:"settings,omitempty"`
	Path        string   `json:"path"`
	Title       string   `json:"title"`
	View        string   `json:"view,omitempty"`
	Module      string   `json:"module,omitempty"`
	Style       string   `json:"style,omitempty"`
	Order       int      `json:"order,omitempty"`
}
