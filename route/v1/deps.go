package v1

import (
	"context"

	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/NimoTech/NimoOS-Search/service/fileindex"
)

// Deps holds wired dependencies for v1 routes. Populated by main.go (T22).
type Deps struct {
	Search    *service.SearchService
	Authz     *service.AuthzService
	NimoOS    RootAuthorizer
	Tools     *service.AgentTools
	Photos    service.ImageSearcher
	Settings  *service.SettingsStore
	FileIndex *fileindex.Subsystem
	// UserServiceURL resolves the UserService base URL (from the runtime
	// discovery file); AdminOnly uses it to check the caller's role.
	UserServiceURL func() (string, error)
}

// RootAuthorizer narrows what routes need from NimoOSClient (testability).
// The authorization source moved from Wiki to core (the main NimoOS
// service), see Task 8.
type RootAuthorizer interface {
	SearchRoots(ctx context.Context, userID string) ([]string, error)
}
