package v1

import (
	"context"

	"github.com/NimoTech/NimoOS-Search/service"
)

// Deps holds wired dependencies for v1 routes. Populated by main.go (T22).
type Deps struct {
	Search *service.SearchService
	Authz  *service.AuthzService
	Wiki   WikiResolver
	Tools  *service.AgentTools
	Photos service.ImageSearcher
}

// WikiResolver narrows what routes need from WikiClient (testability).
type WikiResolver interface {
	UserRoots(ctx context.Context, userID string) ([]string, error)
}
