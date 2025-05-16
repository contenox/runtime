package serverapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/js402/cate/core/llmrepo"
	"github.com/js402/cate/core/runtimestate"
	"github.com/js402/cate/core/serverapi/backendapi"
	"github.com/js402/cate/core/serverapi/chatapi"
	"github.com/js402/cate/core/serverapi/dispatchapi"
	"github.com/js402/cate/core/serverapi/execapi"
	"github.com/js402/cate/core/serverapi/filesapi"
	"github.com/js402/cate/core/serverapi/indexapi"
	"github.com/js402/cate/core/serverapi/poolapi"
	"github.com/js402/cate/core/serverapi/systemapi"
	"github.com/js402/cate/core/serverapi/usersapi"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/vectors"
	"github.com/js402/cate/core/services/accessservice"
	"github.com/js402/cate/core/services/backendservice"
	"github.com/js402/cate/core/services/chatservice"
	"github.com/js402/cate/core/services/dispatchservice"
	"github.com/js402/cate/core/services/downloadservice"
	"github.com/js402/cate/core/services/execservice"
	"github.com/js402/cate/core/services/fileservice"
	"github.com/js402/cate/core/services/indexservice"
	"github.com/js402/cate/core/services/modelservice"
	"github.com/js402/cate/core/services/poolservice"
	"github.com/js402/cate/core/services/tokenizerservice"
	"github.com/js402/cate/core/services/userservice"
	"github.com/js402/cate/core/taskengine"
	"github.com/js402/cate/libs/libauth"
	"github.com/js402/cate/libs/libbus"
	"github.com/js402/cate/libs/libdb"
	"github.com/js402/cate/libs/libroutine"
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
	backendService := backendservice.New(dbInstance)
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
	filesapi.AddFileRoutes(mux, config, fileService)
	downloadService := downloadservice.New(dbInstance, pubsub)
	backendapi.AddQueueRoutes(mux, config, downloadService)
	modelService := modelservice.New(dbInstance, config)
	backendapi.AddModelRoutes(mux, config, modelService, downloadService)
	tokenizerSvc, cleanup, err := tokenizerservice.NewGRPCTokenizer(ctx, tokenizerservice.ConfigGRPC{
		ServerAddress: config.TokenizerServiceURL,
	})
	if err != nil {
		return nil, cleanup, err
	}
	chatService := chatservice.New(state, dbInstance, tokenizerSvc)
	chatapi.AddChatRoutes(mux, config, chatService, state)
	userService := userservice.New(dbInstance, config)
	usersapi.AddUserRoutes(mux, config, userService)

	accessService := accessservice.New(dbInstance)
	usersapi.AddAccessRoutes(mux, config, accessService)
	indexService := indexservice.New(ctx, embedder, execmodelrepo, vectorStore, dbInstance)
	indexapi.AddIndexRoutes(mux, config, indexService)

	execService := execservice.NewExec(ctx, execmodelrepo, dbInstance)
	taskService := execservice.NewTasksEnv(ctx, environmentExec, dbInstance)
	execapi.AddExecRoutes(mux, config, execService, taskService)
	usersapi.AddAuthRoutes(mux, userService)
	dispatchService := dispatchservice.New(dbInstance, config)
	dispatchapi.AddDispatchRoutes(mux, config, dispatchService)
	handler = enableCORS(config, handler)
	handler = jwtRefreshMiddleware(config, handler)
	handler = authSourceNormalizerMiddleware(handler)
	handler = JWTMiddleware(config, handler)
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
