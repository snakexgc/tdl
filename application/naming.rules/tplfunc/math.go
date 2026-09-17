package tplfunc

import (
	"fmt"
	"math/rand"
	"text/template"
)

var Math = []Func{Rand()}

func Rand() Func {
	return func(funcMap template.FuncMap) {
		funcMap["rand"] = func(min, max int) (int, error) {
			if max <= min {
				return 0, fmt.Errorf("rand() requires max (%d) to be greater than min (%d)", max, min)
			}
			// Package-level math/rand functions are safe for concurrent use.
			return rand.Intn(max-min) + min, nil
		}
	}
}
