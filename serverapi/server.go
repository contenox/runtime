package serverapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/js402/CATE/libs/libauth"
	"github.com/js402/CATE/libs/libbus"
	"github.com/js402/CATE/libs/libdb"
	"github.com/js402/CATE/libs/libroutine"
	"github.com/js402/CATE/runtimestate"
	"github.com/js402/CATE/serverapi/backendapi"
	"github.com/js402/CATE/serverapi/chatapi"
	"github.com/js402/CATE/serverapi/poolapi"
	"github.com/js402/CATE/serverapi/systemapi"
	"github.com/js402/CATE/serverapi/usersapi"
	"github.com/js402/CATE/serverops"
	"github.com/js402/CATE/serverops/messagerepo"
	"github.com/js402/CATE/services/accessservice"
	"github.com/js402/CATE/services/backendservice"
	"github.com/js402/CATE/services/chatservice"
	"github.com/js402/CATE/services/downloadservice"
	"github.com/js402/CATE/services/fileservice"
	"github.com/js402/CATE/services/modelservice"
	"github.com/js402/CATE/services/poolservice"
	"github.com/js402/CATE/services/tokenizerservice"
	"github.com/js402/CATE/services/userservice"
)

func New(
	ctx context.Context,
	config *serverops.Config,
	dbInstance libdb.DBManager,
	pubsub libbus.Messenger,
	bus messagerepo.Store,
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
	state, err := runtimestate.New(ctx, dbInstance, pubsub)
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
	// fileservice.
	downloadService := downloadservice.New(dbInstance, pubsub)
	backendapi.AddQueueRoutes(mux, config, downloadService)
	modelService := modelservice.New(dbInstance)
	backendapi.AddModelRoutes(mux, config, modelService, downloadService)
	tokenizerSvc, cleanup, err := tokenizerservice.NewGRPCTokenizer(ctx, tokenizerservice.ConfigGRPC{
		ServerAddress: config.TokenizerServiceURL,
	})
	if err != nil {
		return nil, cleanup, err
	}
	chatService := chatservice.New(state, bus, tokenizerSvc)
	chatapi.AddChatRoutes(mux, config, chatService, state)
	userService := userservice.New(dbInstance, config)
	usersapi.AddUserRoutes(mux, config, userService)

	accessService := accessservice.New(dbInstance)
	usersapi.AddAccessRoutes(mux, config, accessService)

	usersapi.AddAuthRoutes(mux, userService)

	handler = enableCORS(config, handler)
	handler = jwtRefreshMiddleware(config, handler)
	handler = authSourceNormalizerMiddleware(handler)
	handler = jwtMiddleware(config, handler)
	services := []serverops.ServiceMeta{
		modelService,
		backendService,
		chatService,
		accessService,
		userService,
		downloadService,
		fileService,
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

func jwtMiddleware(_ *serverops.Config, next http.Handler) http.Handler {
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
