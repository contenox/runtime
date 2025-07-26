# Structure

```bash
├── compose.yaml
├── Dockerfile.core
├── Dockerfile.tokenizer
├── Dockerfile.vald
├── Dockerfile.worker
├── lerna.json
├── LICENSE
├── Makefile
├── package.json
├── package-lock.json
├── pyrightconfig.json
├── README.md
├── start_worker.py
├── STRUCTURE.md
├── tsconfig.json
├── yarn.lock
```

- `compose.yaml`: Defines the wiring of the infrastructure. Use via Makefile!
- `lerna.json`, `package.json`, `yarn.lock`, `packages/`: This is for building the frontend and UI library components.
- `Makefile`: Contains commands for building, testing, running, or deploying parts of the project.
- `README.md`: Have a look if you have not already.
- `LICENSE`: APACHE 2.0!
- `pyrightconfig.json`: This is for linting the Python codebase.

## Platform's Core (`core`)
Provides shared utilities, interfaces, and implementations for operational concerns cutting across services.

**Language**: `Go`

```bash
├── core
│   ├── go.mod
│   ├── go.sum
│   ├── llmresolver
│   │   ├── llmresolver.go
│   │   └── llmresolver_test.go
│   ├── main.go
│   ├── ...
│   │   ├── ...
│   ├── taskengine
│   │   ├── taskenv.go
│   │   ├── taskexec.go
│   │   └── tasktype.go
```

### Transport Layer (`serverapi`)
Defines the HTTP API endpoints. Not all API routes are (or have to be) exposed by the core.
The API layers tasks are only encoding, error translation, and exposing services. Routes don't have to translate a service 1:1; they can and should combine multiple services to provide a supporting API.
It's modularized by functionality:
- `backendapi`: Routes for managing backend configurations, models, downloads (`/backend`, `/models`, `/downloads`).
- `chatapi`: Routes for chat functionality (`/chat`).
- `dispatchapi`: Routes for leasing and the lifecycle of jobs for workers.
- `filesapi`: Routes for file uploads/management (`/files`).
- `indexapi`: Routes for indexing and embedding (`/index`).
- `poolapi`: Routes related to managing resource pools (likely model pools) (`/pool`).
- `systemapi`: Routes for system information/status (`/system`).
- `tokenizerapi`: Handles tokenization requests. It uses gRPC for communication.
- `usersapi`: Routes for user management, authentication, and access control (`/users`, `/auth`, `/access`).
- `botservice`: Manages bot configurations and integrations
- `chainservice`: Handles workflow chain definitions
- `execservice`: Executes runtime tasks and environments
- `githubservice`: GitHub integration and repo processing
- `telegramservice`: Telegram bot integration


```bash
│   ├── serverapi
│   │   ├── backendapi
│   │   │   ├── backendroutes.go
│   │   │   ├── downloadroutes.go
│   │   │   └── modelroutes.go
│   │   ├── chatapi
│   │   │   └── chatroutes.go
│   │   ├── filesapi
│   │   │   └── filesroutes.go
│   │   ├── poolapi
│   │   │   └── poolroutes.go
│   │   ├── server.go
│   │   ├── workerpipe_test.go
```

System integration tests and the core server setup are also located in the `serverapi` module.

### Business Logic/Services (`services`)
Contains the core logic for each functional area, orchestrating operations. Each service corresponds to an API module (e.g., `chatservice`, `userservice`, `modelservice`, `filesservice`, `poolservice`, `tokenizerservice`, `dispatchservice`, `downloadservice`, `indexservice`).
Services enforce authorization and authentication enforcement as requested by the service requirements. Also, services orchestrate DB calls via transactions if needed.
Data validation, which is not enforced via DB schema, is also handled here. Services should never use other services.

```bash
│   └── services
│       ├── accessservice
│       │   └── accessservice.go
│       ├── backendservice
│       │   └── service.go
│       ├── chatservice
│       │   ├── chatservice.go
│       │   ├── chatservice_test.go
```

A service can be decorated with additional functionality, such as the activity tracker/hooks from `serverops`.
This is intended to prevent changing a service implementation to add additional functionality and allow extension
of the system with features like tracing depending on the required operational logic.

### Operational Logic (`serverops`)
```bash
│   ├── serverops
│   │   ├── auth.go
│   │   ├── config.go
│   │   ├── encode.go
│   │   ├── errors.go
```
Provides supporting functions and common interfaces for the server and services:
- `activitytracker.go`: Provides a mechanism for attaching tracking system activity to services.
- `auth.go`: Authentication/authorization helpers.
- `config.go`: Configuration loading/management.
- `llmclients.go`: Clients for interacting with LLMs.
- `servicemanager.go`: Mainly manages configuration of the services.

#### `store`
Data Persistence Layer. Interacts with the database(s).
Contains functions to manage: users, models, files, backends, accesslists, jobqueue, etc.
The `schema.sql` PostgreSQL, managed by `libdb/postgres.go`.

```bash
│   │   └── store
│   │       ├── accesslists.go
│   │       ├── accesslists_test.go
│   │       ├── ...
│   │       ├── schema.sql
│   │       ├── store.go
│   │       ├── store_test.go
│   │       ├── users.go
│   │       └── users_test.go
```

### Vector Store (`vectors`)
Handles interaction with the Vald vector database. This component is responsible for storing, retrieving, and searching high-dimensional vector embeddings, primarily used for semantic search (RAG).

```bash
│   │   └── vectors
│   │       ├── testing.go
│   │       ├── vectors.go
│   │       └── vectors_test.go
```

## LLM Integration (`llmresolver`, `modelprovider`)
Handles resolving and providing access to Large Language Models (LLMs). The presence of ollamachatclient indicates direct integration with Ollama.

```bash
│   ├── modelprovider
│   │   ├── fromruntimestate.go
│   │   ├── fromruntimestate_test.go
│   │   ├── mocklibmodelprovider.go
│   │   ├── libmodelprovider.go
│   │   ├── ollamachatclient.go
│   │   └── ollamachatclient_test.go
```

## Runtime State (`runtimestate`)
Manages reconciling the ollama backend to match the desired state, including model downloads.

```bash
│   ├── runtimestate
│   │   ├── downloadqueue.go
│   │   ├── runtimestate_pool_test.go
│   │   ├── state.go
│   │   └── state_test.go
```

## Task Engine (`taskengine`)
```bash
│   ├── taskengine
│   │   ├── taskenv.go
│   │   ├── taskexec.go
│   │   ├── activity.go
│   │   ├── alert.go
│   │   └── tasktype.go
```
The Task Engine contains the state-machine that provides the core capability to define, manage, and execute complex, chained sequences of operations. It is designed to enable automation, including multi-step interactions with Large Language Models (LLMs), conditional logic, and integration with other internal or external systems via hooks.


Thank you for the detailed code and explanation. Based on your input, I will now add a new section to the structure document that explains the **`hookrecipes`** package and its role in the system.

---

## Hook Recipes (`hookrecipes`)
This package provides pre-built compositions of hooks that combine multiple hook functionalities into reusable patterns. These "recipes" enable complex behavior by chaining or configuring simpler hooks together, reducing duplication and increasing flexibility in how tasks are executed.

**Language**: Go
**Purpose**: To provide higher-level logic for task execution by composing existing hooks into structured workflows.

### Key Concepts

- **Hook Composition**: A recipe combines two or more `taskengine.HookRepo` implementations into a single logical operation.
- **Parameterization**: Recipes support runtime configuration through named arguments (e.g., `"top_k"`, `"distance"`).
- **Chaining Execution**: The output of one hook is passed as input to the next, enabling pipelines like search → resolve.

### Structure

```bash
core/hookrecipes/
├── parser.go
├── parser_test.go
├── rag.go
└── rag_test.go
```

#### `SearchThenResolveHook`
A core recipe that chains:
1. A **search hook** to retrieve relevant knowledge vectors.
2. A **resolve hook** to interpret and format the results.

This pattern supports RAG (Retrieval-Augmented Generation) workflows where context is retrieved before being used in an LLM prompt.

##### Features:
- Accepts parameters via hook arguments:
  - `top_k`: Number of top results to return.
  - `epsilon`: Tolerance for similarity matching.
  - `distance`: Threshold for relevance filtering.
  - `position`: Index within result list to focus on.
  - `radius`: Spatial scope for vector search.
- Supports input parsing: allows prefix-based argument extraction from strings.
- Implements interface: `taskengine.HookRepo`

##### Example Usage:

```go
knowledgeHook := hookrecipes.NewSearchThenResolveHook(hookrecipes.SearchThenResolveHook{
    SearchHook:     rag,
    ResolveHook:    hooks.NewSearchResolveHook(dbInstance),
    DefaultTopK:    1,
    DefaultDist:    40,
    DefaultPos:     0,
    DefaultEpsilon: 0.5,
    DefaultRadius:  40,
})
```

Used in a command router or pipeline:

```go
hookMux := hooks.NewMux(map[string]taskengine.HookRepo{
    "echo":             echocmd,
    "search_knowledge": knowledgeHook,
})
```

## Hooks (`hooks`)
The `hooks` package provides modular, pluggable components for executing side effects or external integrations during task execution in the system. These hooks can be composed together to form complex behaviors such as calling external APIs, interacting with databases, or managing chat workflows.

**Language**: Go
**Purpose**: To define reusable actions that can be triggered during a task lifecycle, enabling extensibility and integration with internal/external systems.

### Key Concepts

- **HookRepo Interface**: All hooks implement this interface, which defines two methods:
  - `Supports(ctx context.Context) ([]string, error)` – declares the types of hooks supported.
  - `Exec(...)` – executes the hook logic with input data and returns transformed output along with status.

- **Modular Design**: Each hook is self-contained and can be reused across different parts of the system.

- **Composable**: Hooks can be combined using structures like `Mux` or `SimpleProvider`, allowing multiple hooks to coexist and be dispatched dynamically.

### Structure

```bash
core/hooks/
├── chat.go       echo.go       mockrepo.go     rag.go       searchresolve.go   webhook.go
└── chat_test.go  echo_test.go  mux.go          rag_test.go  simpleprovider.go
```

#### Example Hook Implementations

- `WebCaller` (`webhook.go`):
  Makes HTTP requests to external services (e.g., REST APIs).
  - Supports dynamic method selection (`GET`, `POST`, etc.)
  - Handles query parameters, headers, and JSON payloads.
  - Returns parsed JSON or raw response string based on success/failure.

  ```go
  hook := hooks.NewWebCaller()
  ```

- `ChatHook`:
  Integrates with the chat system to handle message persistence, history retrieval, and LLM invocation.

- `SearchResolveHook`:
  Combines vector search with post-processing logic to support RAG workflows.

- `Mux`:
  Routes to different hooks based on command names (e.g., `/echo`, `/search_knowledge`).

- `SimpleProvider`:
  A registry for named hooks, used to bind them into the task engine.

### Usage Pattern

Hooks are typically registered and used within the task engine setup:

```go
// Create a webhook hook
webhookHook := hooks.NewWebCaller()

// Create a command router
hookMux := hooks.NewMux(map[string]taskengine.HookRepo{
    "echo":             echocmd,
    "search_knowledge": knowledgeHook,
})

// Combine all hooks
hooksRegistry := hooks.NewSimpleProvider(map[string]taskengine.HookRepo{
    "webhook":      webhookHook,
    "command_router": hookMux,
})
```

These hooks are then passed to the task engine to enable dynamic behavior during task execution.

## Dockerfile (`Dockerfile.core`)
Instructions to build the Docker image for this core backend service.

## Frontend
**Framework/Library**: React
**Language**: TypeScript
**Build Tool**: Vite

```bash
├── frontend
│   ├── eslint.config.js
│   ├── index.html
│   ├── nginx.conf
│   ├── package.json
│   ├── public
│   │   └── vite.svg
│   ├── README.md
```

### Structure
- `src/main.tsx`: Entry point for the React application.
- `src/App.tsx`: Root application component.
- `src/components`: Application-specific reusable UI components (Layout, Sidebar, etc.).
- `src/pages`: Components representing different pages/views of the application (e.g., Login, Chat, Admin sections for Users, Backends).
- `src/hooks`: Custom React hooks for fetching data from the backend API and managing frontend state (e.g., useChats, useModels, useLogin).
- `src/lib`: Core frontend utilities, including API interaction (api.ts, Workspace.ts), authentication context (authContext.ts, AuthProvider.tsx), and type definitions (types.ts).
- `src/config`: Routing configuration (routes.tsx).
- `public`: Static assets.
- `nginx.conf`: Nginx: TODO - check if it's necessary.

```bash
├── frontend
│   ├── eslint.config.js
│   ├── index.html
│   ├── nginx.conf
│   ├── package.json
│   ├── public
│   │   └── vite.svg
│   ├── README.md
│   ├── src
│   │   ├── app.css
│   │   ├── App.tsx
│   │   ├── assets
│   │   │   ├── logo.png
│   │   │   └── react.svg
│   │   ├── components
│   │   │   ├── DropdownMenu.tsx
│   │   │   ├── Layout.tsx
│   │   │   ├── ProtectedRoute.tsx
│   │   │   └── sidebar
│   │   │       ├── DesktopSidebar.tsx
│   │   │       ├── MobileSidebar.tsx
│   │   │       ├── SidebarNav.tsx
│   │   │       └── Sidebar.tsx
│   │   ├── config
│   │   │   ├── routeConstants.ts
│   │   │   └── routes.tsx
│   │   ├── hooks
│   │   │   ├── useAccess.ts
│   │   │   ├── ...
│   │   │   └── useUsers.ts
│   │   ├── i18n.ts
│   │   ├── lib
│   │   │   ├── api.ts
│   │   │   ├── authContext.ts
│   │   │   ├── AuthProvider.tsx
│   │   │   ├── fetch.ts
│   │   │   ├── ThemeProvider.tsx
│   │   │   ├── types.ts
│   │   │   └── utils.ts
```

#### UI Library
Uses components from the separate packages/ui library. This component Library is not evaluated as final yet. It may be replaced once rapid prototyping in the UI is not necessary, but currently, it's in and allows pinpointing core differentiators that would require custom feel later on.

```bash
├── packages
│   └── ui
│       ├── package.json
│       ├── postcss.config.mjs
│       ├── public
│       │   └── components.css
│       ├── src
│       │   ├── components
│       │   │   ├── Accordion.tsx
│       │   │   ├── Badge.tsx
│       │   │   ├── ...
│       │   │   └── UserMenu.tsx
│       │   ├── index.css
│       │   ├── index.ts
│       │   └── utils.ts
│       └── tsconfig.json
```

## Tokenizer Service (`tokenizer`)
**Language**: Go
**Purpose**: A separate microservice dedicated to handling text tokenization.
Done separately to potentially scale out and to reduce build time due to CGO requirements.
**Communication**: gRPC to the core; later, the core may expose some features via HTTP.
**Implementation**: Uses libollama for the actual tokenization logic via Ollama.
**Dockerfile** (`Dockerfile.tokenizer`): Corresponding Dockerfile.

```bash
├── tokenizer
│   ├── go.mod
│   ├── go.sum
│   ├── main.go
│   └── service
│       └── service.go
```

## Shared Libraries (`libs`)
```bash
├── libs
│   ├── libauth
│   │   ├── go.mod
│   │   ├── go.sum
│   │   ├── libauth.go
│   │   └── libauth_test.go
│   ├── libbus
```

- `libauth`: Authentication utilities.
- `libbus`: Message bus interface (implemented via NATS) for event data streaming and coordination of async processes, like canceling downloads.
- `libcipher`: Cryptography (hashing, encryption).
- `libdb`: Database abstraction (PostgreSQL).
- `libkv`: Key-Value store abstraction (Valkey/Redis).
- `libollama`: Library for interacting with Ollama features not exposed via ollama-API (tokenization).
- `libroutine`: Goroutine management utilities, like circuit breaker.
- `libtestenv`: Utilities for setting up testing environments for integration tests.
- `libmodelprovider`: Contains the Model provider interface and multiple LLM integrations, like OpenAI, Gemini, vLLM and Ollama.

## API Tests (`apitests`)
```bash
├── apitests
│   ├── conftest.py
│   ├── helpers.py
│   ├── requirements.txt
│   ├── ...
│   └── test_services.py
```

## Workers (`workers`)
```bash
workers/
├── __init__.py
├── parser.py
├── plaintext.py
├── requirements.txt
└── worker.py
```
Workers are responsible for processing Jobs asynchronously, such as parsing and indexing documents, or generating embeddings for text data. They gain Jobs by polling the dispatchapi endpoints and marking them as done when the results are ingested into the core.
