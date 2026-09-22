package ports

import "context"

const SessionCatalogName = "account.sessions"

type SessionOption struct {
	Namespace string `json:"namespace"`
	Current   bool   `json:"current"`
}

type SessionRepository interface {
	List(context.Context) ([]string, error)
	Delete(context.Context, string) (int, error)
}

type SessionCatalog interface {
	List(context.Context) ([]SessionOption, error)
	Delete(context.Context, string) (int, error)
}
