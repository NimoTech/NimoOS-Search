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
}

// RootAuthorizer narrows what routes need from NimoOSClient (testability).
// 授权源已从 Wiki 切到核心(NimoOS 主服务),见 Task 8。
type RootAuthorizer interface {
	SearchRoots(ctx context.Context, userID string) ([]string, error)
}
