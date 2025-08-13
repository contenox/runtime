package hookrecipes

import (
	"github.com/contenox/activitytracker"
	libdb "github.com/contenox/dbexec"
	"github.com/contenox/runtime-mvp/core/chat"
	"github.com/contenox/runtime-mvp/core/hooks"
	"github.com/contenox/runtime-mvp/core/serverops/vectors"
	"github.com/contenox/runtime/embedservice"
	"github.com/contenox/runtime/taskengine"
)

// HookDependencies contains all required dependencies for legacy hooks
type HookDependencies struct {
	DBInstance      libdb.DBManager
	ChatManager     *chat.Manager
	Embedder        embedservice.Service
	VectorStore     vectors.Store
	ActivityTracker activitytracker.ActivityTracker
}

// NewLegacyHooks initializes all legacy hooks with their dependencies
func NewLegacyHooks(deps HookDependencies) map[string]taskengine.HookRepo {
	toRegister := make(map[string]taskengine.HookRepo)

	// Initialize individual hooks
	echoHook := hooks.NewEchoHook()
	chatHook := hooks.NewChatHook(deps.DBInstance, deps.ChatManager)
	searchResolveHook := hooks.NewSearchResolveHook(deps.DBInstance)
	searchHook := hooks.NewSearch(deps.Embedder, deps.VectorStore, deps.DBInstance)
	webCaller := hooks.NewWebCaller()

	// Create command router with sub-commands
	muxCommands := map[string]taskengine.HookRepo{
		"echo":   echoHook,
		"search": searchResolveHook,
	}
	muxHook := hooks.NewMux(muxCommands, deps.ActivityTracker)

	// Create composite hooks
	searchThenResolveHook := NewSearchThenResolveHook(
		SearchThenResolveHook{
			SearchHook:     searchHook,
			ResolveHook:    searchResolveHook,
			DefaultTopK:    3,
			DefaultDist:    0.5,
			DefaultPos:     0,
			DefaultEpsilon: 0.01,
			DefaultRadius:  0.03,
		},
		deps.ActivityTracker,
	)

	// Register all hooks
	toRegister["echo"] = echoHook
	toRegister["command_router"] = muxHook
	toRegister["convert_openai_to_history"] = chatHook
	toRegister["convert_history_to_openai"] = chatHook
	toRegister["resolve_search_result"] = searchResolveHook
	toRegister["vector_search"] = searchHook
	toRegister["webhook"] = webCaller
	toRegister["search_knowledge"] = searchThenResolveHook

	return toRegister
}
