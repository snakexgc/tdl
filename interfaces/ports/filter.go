package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const FilterRulesName = "filter.rules"

type FilterInput struct {
	Account types.AccountID
	Name    string
	Size    int64
}

type Reason string

const (
	Allowed           Reason = "allowed"
	ExtensionExcluded Reason = "extension_excluded"
	SizeExcluded      Reason = "size_excluded"
)

type FilterRules interface {
	ShouldHandle(context.Context, FilterInput) (bool, Reason)
}
