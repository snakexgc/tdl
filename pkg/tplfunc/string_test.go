package tplfunc

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"text/template"
)

const (
	testCaseEmpty    = "empty"
	testString       = "test"
	testStringUpper  = "TEST"
	testCaseLower    = "lower"
	testCaseUpper    = "upper"
	testStringSnake  = "test_test"
	testStringPascal = "TestTest"
	testCasePascal   = "pascal"
)

func stringSlice(args []string) string {
	s := make([]string, len(args))
	for i, v := range args {
		s[i] = fmt.Sprintf(`"%s"`, v)
	}
	return strings.Join(s, " ")
}

func TestRepeat(t *testing.T) {
	tests := []struct {
		name string
		S    string
		N    int
		want string
	}{
		{name: testCaseEmpty, S: "", N: 0, want: ""},
		{name: "zero", S: testString, N: 0, want: ""},
		{name: "one", S: testString, N: 1, want: testString},
		{name: "two", S: testString, N: 2, want: "testtest"},
	}

	m := FuncMap(Repeat())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Builder{}

			err := template.Must(template.New("test").
				Funcs(m).
				Parse(`{{ repeat .S .N }}`)).
				Execute(&got, tt)
			if err != nil {
				t.Errorf("repeat() error = %v", err)
				return
			}
			if got.String() != tt.want {
				t.Errorf("repeat() got = %v, want %v", got.String(), tt.want)
			}
		})
	}
}

func TestReplace(t *testing.T) {
	tests := []struct {
		name  string
		S     string
		Pairs []string
		want  string
	}{
		{name: testCaseEmpty, S: "", Pairs: nil, want: ""},
		{name: "empty pairs", S: testString, Pairs: nil, want: testString},
		{name: "single pair", S: testString, Pairs: []string{"t", "T"}, want: "TesT"},
		{name: "multiple pairs1", S: testString, Pairs: []string{"t", "T", "e", "E"}, want: "TEsT"},
		{name: "multiple pairs2", S: testString, Pairs: []string{"t", "T", "e", "E", "s", "S"}, want: testStringUpper},
	}

	m := FuncMap(Replace())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Builder{}

			err := template.Must(template.New("test").
				Funcs(m).
				Parse(fmt.Sprintf(`{{ replace .S %s }}`, stringSlice(tt.Pairs)))).
				Execute(&got, tt)
			if err != nil {
				t.Errorf("replace() error = %v", err)
				return
			}
			if got.String() != tt.want {
				t.Errorf("replace() got = %v, want %v", got.String(), tt.want)
			}
		})
	}
}

func TestReplacePanic(t *testing.T) {
	tests := []struct {
		name  string
		S     string
		Pairs []string
	}{
		{name: "odd pairs", S: testString, Pairs: []string{"t", "T", "e"}},
	}

	m := FuncMap(Replace())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := template.Must(template.New("test").
				Funcs(m).
				Parse(fmt.Sprintf(`{{ replace .S %s }}`, stringSlice(tt.Pairs)))).
				Execute(io.Discard, tt)
			if err == nil {
				t.Errorf("replace() expected error")
			}
		})
	}
}

func TestToUpper(t *testing.T) {
	tests := []struct {
		name string
		S    string
		want string
	}{
		{name: testCaseEmpty, S: "", want: ""},
		{name: testCaseLower, S: testString, want: testStringUpper},
		{name: testCaseUpper, S: testStringUpper, want: testStringUpper},
	}

	m := FuncMap(ToUpper())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Builder{}

			err := template.Must(template.New("test").
				Funcs(m).
				Parse(`{{ upper .S }}`)).
				Execute(&got, tt)
			if err != nil {
				t.Errorf("upper() error = %v", err)
				return
			}
			if got.String() != tt.want {
				t.Errorf("upper() got = %v, want %v", got.String(), tt.want)
			}
		})
	}
}

func TestToLower(t *testing.T) {
	tests := []struct {
		name string
		S    string
		want string
	}{
		{name: testCaseEmpty, S: "", want: ""},
		{name: testCaseLower, S: testString, want: testString},
		{name: testCaseUpper, S: testStringUpper, want: testString},
	}

	m := FuncMap(ToLower())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Builder{}

			err := template.Must(template.New("test").
				Funcs(m).
				Parse(`{{ lower .S }}`)).
				Execute(&got, tt)
			if err != nil {
				t.Errorf("lower() error = %v", err)
				return
			}
			if got.String() != tt.want {
				t.Errorf("lower() got = %v, want %v", got.String(), tt.want)
			}
		})
	}
}

func TestSnakeCase(t *testing.T) {
	tests := []struct {
		name string
		S    string
		want string
	}{
		{name: testCaseEmpty, S: "", want: ""},
		{name: testCaseLower, S: testString, want: testString},
		{name: testCaseUpper, S: testStringUpper, want: testString},
		{name: "camel", S: "testTest", want: testStringSnake},
		{name: testCasePascal, S: testStringPascal, want: testStringSnake},
	}

	m := FuncMap(SnakeCase())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Builder{}

			err := template.Must(template.New("test").
				Funcs(m).
				Parse(`{{ snakecase .S }}`)).
				Execute(&got, tt)
			if err != nil {
				t.Errorf("snakecase() error = %v", err)
				return
			}
			if got.String() != tt.want {
				t.Errorf("snakecase() got = %v, want %v", got.String(), tt.want)
			}
		})
	}
}

func TestCamelCase(t *testing.T) {
	tests := []struct {
		name string
		S    string
		want string
	}{
		{name: testCaseEmpty, S: "", want: ""},
		{name: testCaseLower, S: testString, want: "Test"},
		{name: testCaseUpper, S: testStringUpper, want: "Test"},
		{name: "snake", S: testStringSnake, want: testStringPascal},
		{name: "pascal", S: testStringPascal, want: testStringPascal},
	}

	m := FuncMap(CamelCase())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Builder{}

			err := template.Must(template.New("test").
				Funcs(m).
				Parse(`{{ camelcase .S }}`)).
				Execute(&got, tt)
			if err != nil {
				t.Errorf("camelcase() error = %v", err)
				return
			}
			if got.String() != tt.want {
				t.Errorf("camelcase() got = %v, want %v", got.String(), tt.want)
			}
		})
	}
}

func TestKebabCase(t *testing.T) {
	tests := []struct {
		name string
		S    string
		want string
	}{
		{name: testCaseEmpty, S: "", want: ""},
		{name: testCaseLower, S: testString, want: testString},
		{name: testCaseUpper, S: testStringUpper, want: testString},
		{name: "camel", S: "testTest", want: "test-test"},
		{name: "pascal", S: testStringPascal, want: "test-test"},
	}

	m := FuncMap(KebabCase())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Builder{}

			err := template.Must(template.New("test").
				Funcs(m).
				Parse(`{{ kebabcase .S }}`)).
				Execute(&got, tt)
			if err != nil {
				t.Errorf("kebabcase() error = %v", err)
				return
			}
			if got.String() != tt.want {
				t.Errorf("kebabcase() got = %v, want %v", got.String(), tt.want)
			}
		})
	}
}
