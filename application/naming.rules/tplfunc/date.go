package tplfunc

import (
	"fmt"
	"text/template"
	"time"

	"github.com/spf13/cast"
)

var Date = []Func{Now(), FormatDate()}

func Now() Func {
	return func(funcMap template.FuncMap) {
		funcMap["now"] = func() int64 {
			return time.Now().Unix()
		}
	}
}

func FormatDate() Func {
	return func(funcMap template.FuncMap) {
		funcMap["formatDate"] = func(args ...any) (string, error) {
			switch len(args) {
			case 0:
				return "", fmt.Errorf("formatDate() requires at least 1 argument")
			case 1:
				return time.Unix(cast.ToInt64(args[0]), 0).Format("20060102150405"), nil
			case 2:
				format, ok := args[1].(string)
				if !ok {
					return "", fmt.Errorf("formatDate() format must be a string, got %T", args[1])
				}
				return time.Unix(cast.ToInt64(args[0]), 0).Format(format), nil
			default:
				return "", fmt.Errorf("formatDate() requires at most 2 arguments")
			}
		}
	}
}
