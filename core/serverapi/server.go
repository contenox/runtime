package serverapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/contenox/runtime-mvp/core/chat"
	"github.com/contenox/runtime-mvp/core/llmrepo"
	"github.com/contenox/runtime-mvp/core/runtimestate"
	"github.com/contenox/runtime-mvp/core/serverapi/activityapi"
	"github.com/contenox/runtime-mvp/core/serverapi/backendapi"
	"github.com/contenox/runtime-mvp/core/serverapi/chainsapi"
	"github.com/contenox/runtime-mvp/core/serverapi/chatapi"
	"github.com/contenox/runtime-mvp/core/serverapi/dispatchapi"
	"github.com/contenox/runtime-mvp/core/serverapi/execapi"
	"github.com/contenox/runtime-mvp/core/serverapi/filesapi"
	"github.com/contenox/runtime-mvp/core/serverapi/githubapi"
	"github.com/contenox/runtime-mvp/core/serverapi/indexapi"
	"github.com/contenox/runtime-mvp/core/serverapi/poolapi"
	providersapi "github.com/contenox/runtime-mvp/core/serverapi/providerapi"
	"github.com/contenox/runtime-mvp/core/serverapi/systemapi"
	"github.com/contenox/runtime-mvp/core/serverapi/telegramapi"
	"github.com/contenox/runtime-mvp/core/serverapi/usersapi"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/vectors"
	"github.com/contenox/runtime-mvp/core/services/accessservice"
	"github.com/contenox/runtime-mvp/core/services/activityservice"
	"github.com/contenox/runtime-mvp/core/services/backendservice"
	"github.com/contenox/runtime-mvp/core/services/chainservice"
	"github.com/contenox/runtime-mvp/core/services/chatservice"
	"github.com/contenox/runtime-mvp/core/services/dispatchservice"
	"github.com/contenox/runtime-mvp/core/services/downloadservice"
	"github.com/contenox/runtime-mvp/core/services/execservice"
	"github.com/contenox/runtime-mvp/core/services/fileservice"
	"github.com/contenox/runtime-mvp/core/services/githubservice"
	"github.com/contenox/runtime-mvp/core/services/indexservice"
	"github.com/contenox/runtime-mvp/core/services/modelservice"
	"github.com/contenox/runtime-mvp/core/services/poolservice"
	"github.com/contenox/runtime-mvp/core/services/providerservice"
	"github.com/contenox/runtime-mvp/core/services/telegramservice"
	"github.com/contenox/runtime-mvp/core/services/userservice"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/libs/libauth"
	"github.com/contenox/runtime-mvp/libs/libbus"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/contenox/runtime-mvp/libs/libkv"
	"github.com/contenox/runtime-mvp/libs/libroutine"
	"github.com/google/uuid"
)

func New(
	ctx context.Context,
	config *serverops.Config,
	dbInstance libdb.DBManager,
	pubsub libbus.Messenger,
	embedder llmrepo.ModelRepo,
	execmodelrepo llmrepo.ModelRepo,
	environmentExec taskengine.EnvExecutor,
	state *runtimestate.State,
	vectorStore vectors.Store,
	hookRegistry taskengine.HookRegistry,
	chatManager *chat.Manager,
	kvManager libkv.KVManager,
) (http.Handler, func() error, error) {
	cleanup := func() error { return nil }
	mux := http.NewServeMux()
	var handler http.Handler = mux
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		// OK
	})
	err := serverops.NewServiceManager(config)
	if err != nil {
		return nil, cleanup, err
	}
	tracker := taskengine.NewKVActivityTracker(kvManager)
	stdOuttracker := serverops.NewLogActivityTracker(slog.Default())
	serveropsChainedTracker := serverops.ChainedTracker{
		tracker,
		stdOuttracker,
	}
	backendService := backendservice.New(dbInstance)
	backendService = backendservice.WithActivityTracker(backendService, serveropsChainedTracker)
	backendapi.AddBackendRoutes(mux, config, backendService, state)
	poolservice := poolservice.New(dbInstance)
	poolapi.AddPoolRoutes(mux, config, poolservice)
	// Get circuit breaker pool instance
	pool := libroutine.GetPool()

	// Start managed loops using the pool
	pool.StartLoop(
		ctx,
		"backendCycle",        // unique key for this operation
		3,                     // failure threshold
		10*time.Second,        // reset timeout
		10*time.Second,        // interval
		state.RunBackendCycle, // operation
	)

	pool.StartLoop(
		ctx,
		"downloadCycle",        // unique key for this operation
		3,                      // failure threshold
		10*time.Second,         // reset timeout
		10*time.Second,         // interval
		state.RunDownloadCycle, // operation
	)
	fileService := fileservice.New(dbInstance, config)
	fileService = fileservice.WithActivityTracker(fileService, fileservice.NewFileVectorizationJobCreator(dbInstance))
	fileService = fileservice.WithActivityTracker(fileService, serveropsChainedTracker)
	filesapi.AddFileRoutes(mux, config, fileService)
	downloadService := downloadservice.New(dbInstance, pubsub)
	downloadService = downloadservice.WithActivityTracker(downloadService, serveropsChainedTracker)
	backendapi.AddQueueRoutes(mux, config, downloadService)
	modelService := modelservice.New(dbInstance, config)
	modelService = modelservice.WithActivityTracker(modelService, serveropsChainedTracker)
	backendapi.AddModelRoutes(mux, config, modelService, downloadService)

	chatService := chatservice.New(dbInstance, environmentExec)
	chatService = chatservice.WithActivityTracker(chatService, serveropsChainedTracker)
	chatapi.AddChatRoutes(mux, config, chatService, state)
	userService := userservice.New(dbInstance, config)
	userService = userservice.WithActivityTracker(userService, serveropsChainedTracker)
	usersapi.AddUserRoutes(mux, config, userService)

	accessService := accessservice.New(dbInstance)
	accessService = accessservice.WithActivityTracker(accessService, serveropsChainedTracker)
	usersapi.AddAccessRoutes(mux, config, accessService)
	indexService := indexservice.New(ctx, embedder, execmodelrepo, vectorStore, dbInstance)

	indexService = indexservice.WithActivityTracker(indexService, serveropsChainedTracker)
	indexapi.AddIndexRoutes(mux, config, indexService)

	execService := execservice.NewExec(ctx, execmodelrepo, dbInstance)
	execService = execservice.WithActivityTracker(execService, serveropsChainedTracker)
	taskService := execservice.NewTasksEnv(ctx, environmentExec, dbInstance, hookRegistry)
	execapi.AddExecRoutes(mux, config, execService, taskService)
	usersapi.AddAuthRoutes(mux, userService)
	dispatchService := dispatchservice.New(dbInstance, config)
	dispatchapi.AddDispatchRoutes(mux, config, dispatchService)
	providerService := providerservice.New(dbInstance)
	providerService = providerservice.WithActivityTracker(providerService, serveropsChainedTracker)
	providersapi.AddProviderRoutes(mux, config, providerService)
	activityService := activityservice.New(tracker, taskengine.NewAlertSink(kvManager))
	activityapi.AddActivityRoutes(mux, config, activityService)
	githubService := githubservice.New(dbInstance)
	githubService = githubservice.WithActivityTracker(githubService, serveropsChainedTracker)
	githubapi.AddGitHubRoutes(mux, config, githubService)
	githubworker := githubservice.NewWorker(githubService, kvManager, tracker, dbInstance)
	libroutine.GetPool().StartLoop(ctx, "github-worker-pull", 2, time.Minute, time.Minute, func(ctx context.Context) error {
		return githubworker.ReceiveTick(ctx)
	})
	libroutine.GetPool().StartLoop(ctx, "github-worker-sync", 2, time.Minute, time.Minute, func(ctx context.Context) error {
		return githubworker.ProcessTick(ctx)
	})

	chainService := chainservice.New(dbInstance)
	chainsapi.AddChainRoutes(mux, config, chainService)

	telegramService := telegramservice.New(dbInstance)
	telegramService = telegramservice.WithServiceActivityTracker(telegramService, serveropsChainedTracker)
	telegramapi.AddTelegramRoutes(mux, telegramService)

	handler = enableCORS(config, handler)
	handler = requestIDMiddleware(config, handler)
	handler = jwtRefreshMiddleware(config, handler)
	handler = authSourceNormalizerMiddleware(handler)
	handler = JWTMiddleware(config, handler)
	handler = rateLimitMiddleware(kvManager, 100, time.Minute)(handler)
	services := []serverops.ServiceMeta{
		modelService,
		backendService,
		chatService,
		accessService,
		userService,
		downloadService,
		fileService,
		indexService,
		dispatchService,
		execService,
		providerService,
		chainService,
		activityService,
		githubService,
	}
	err = serverops.GetManagerInstance().RegisterServices(services...)
	if err != nil {
		return nil, cleanup, err
	}
	systemapi.AddRoutes(mux, config, serverops.GetManagerInstance())

	return handler, cleanup, nil
}

func enableCORS(cfg *serverops.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqOrigin := r.Header.Get("Origin")
		allowedOrigin := ""
		if len(reqOrigin) > 0 {
			w.Header().Add("Vary", "Origin")
		}
		// If the config explicitly allows all origins.
		declaredOrigins := strings.Split(cfg.AllowedAPIOrigins, ",")
		for _, o := range declaredOrigins {
			if strings.TrimSpace(o) == "*" {
				allowedOrigin = "*"
				break
			}
		}

		// If not, check if the request origin is in the allowed list.
		if allowedOrigin == "" && reqOrigin != "" {
			for _, o := range declaredOrigins {
				if reqOrigin == strings.TrimSpace(o) {
					allowedOrigin = reqOrigin
					break
				}
			}
		}
		proxies := strings.Split(cfg.ProxyOrigin, ",")
		isProxy := false
		for _, proxy := range proxies {
			if proxy == reqOrigin {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Origin", proxy)
				isProxy = true
				break
			}
		}
		// Set the header only if an allowed origin was found.
		if allowedOrigin != "" && !isProxy {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		}

		w.Header().Set("Access-Control-Allow-Methods", cfg.AllowedMethods)
		w.Header().Set("Access-Control-Allow-Headers", cfg.AllowedHeaders)

		// Handle preflight requests.
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func authSourceNormalizerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		hasBearerToken := authHeader != "" && strings.HasPrefix(strings.ToLower(authHeader), "bearer ") && len(strings.Fields(authHeader)) > 1
		ctx := r.Context()
		if !hasBearerToken {
			cookie, err := r.Cookie("auth_token")
			if err == nil && cookie != nil && cookie.Value != "" {
				ctx = context.WithValue(r.Context(), libauth.ContextTokenKey, cookie.Value)
			}

		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func JWTMiddleware(_ *serverops.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if len(r.Header.Get("Authorization")) > 0 {
			tokenString := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			ctx = context.WithValue(r.Context(), libauth.ContextTokenKey, tokenString)
		}
		if next == nil {
			next = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				err := fmt.Errorf("SERVER BUG: middleware error next is nil")
				serverops.Error(w, r, err, serverops.ServerOperation)
			})
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func jwtRefreshMiddleware(_ *serverops.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if the request comes from a browser (for example, via User-Agent header)
		if len(r.Header.Get("User-Agent")) > 0 {
			// Try to refresh the token (RefreshToken returns the new token, a bool if it was replaced, and error)
			newToken, replaced, expiresAt, err := serverops.RefreshToken(r.Context())
			if err != nil {
				// now we silently ignore and continue with the old token.
			} else if replaced {
				// Create a new cookie with the updated token
				cookie := &http.Cookie{
					Name:     "auth_token",
					Value:    newToken,
					Path:     "/",
					Expires:  expiresAt.UTC(),
					SameSite: http.SameSiteStrictMode,
					HttpOnly: true,
					Secure:   false,
				}
				http.SetCookie(w, cookie)

				// Update the request context with the new token so downstream middleware/handlers use it.
				r = r.WithContext(context.WithValue(r.Context(), libauth.ContextTokenKey, newToken))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func requestIDMiddleware(_ *serverops.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.New().String()
		ctx := context.WithValue(r.Context(), serverops.ContextKeyRequestID, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func rateLimitMiddleware(kvManager libkv.KVManager, limit int, window time.Duration) func(http.Handler) http.Handler {
	rateLimiter := serverops.NewRateLimiter(kvManager)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip rate limiting for health checks
			if r.URL.Path == "/health" {
				next.ServeHTTP(w, r)
				return
			}

			key := r.RemoteAddr
			allowed, err := rateLimiter.Allow(r.Context(), key, limit, window)
			if err != nil {
				serverops.Error(w, r, fmt.Errorf("rate limit error"), serverops.ServerOperation)
				return
			}

			if !allowed {
				serverops.Error(w, r, fmt.Errorf("too many requests"), serverops.ServerOperation)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
