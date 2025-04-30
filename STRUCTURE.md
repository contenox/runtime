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
- `pyrightconfig.json` This is for linting the python codebase.

## Platform's core (`core`)
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
│   ├── modelprovider
│   │   ├── fromruntimestate.go
│   │   ├── fromruntimestate_test.go
│   │   ├── mockmodelprovider.go
```

### Transport Layer (`serverapi`)
Defines the HTTP API endpoints. Not all api-routes are (or have to be) exposed by the core.
The API layers tasks are only encoding, error translation and exposing services. Routes don't have to translate a service 1:1 they can and should combine multiple services to provide a supporting API.

It's modularized by functionality:

- `backendapi`: Routes for managing backend configurations, models, downloads (`/backend`, `/models`, `/downloads`).
- `chatapi`: Routes for chat functionality (`/chat`).
- `dispatchapi`: Routes for leasing and the lifecycle of jobs for workers.
- `filesapi`: Routes for file uploads/management (`/files`).
- `indexapi`: Routes for indexing and embedding (`/index`).
- `poolapi`: Routes related to managing resource pools (likely model pools) (`/pool`).
- `systemapi`: Routes for system information/status (`/system`).
- `tokenizerapi`: Handles tokenization requests. it uses gRPC for communication.
- `usersapi`: Routes for user management, authentication, and access control (`/users`, `/auth`, `/access`).

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

system integration tests and the core server setup are also located in the `serverapi` module.

### Business Logic/Services (`services`)

Contains the core logic for each functional area, orchestrating operations. Each service corresponds to an API module (e.g., `chatservice`, `userservice`, `modelservice`, `filesservice`, `poolservice`, `tokenizerservice`, `dispatchservice`, `downloadservice`, `indexservice`).
services enforce authorization and authentication enforcement as requests by the service requirements. Also services orchistrate db-calls via transactions if needet.
Data validation, which is not enforced via DB-schema is also handled here. Services should never use other services.

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
Which is intended to prevent changing a service implementation to add additional functionality and allow extension
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
- `servicemanager.go`: mainly manages configuration of the services.

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
│   │   ├── mockmodelprovider.go
│   │   ├── modelprovider.go
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

Uses components from the separate packages/ui library. This component Library is not evaluated as final yet. It may be replaces once's rapid prototyping in the UI
is not necessary, but currently it's in and allows to pinpoint core differentiators that would require later on custom fool and feel.

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
**Communication**: gRPC to the core later core may expose some features via HTTP.
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
 - `libbus`: message bus interface (implemented via NATS) for event data streaming and cordination of async processes, like canceling downloads.
 - `libcipher`: Cryptography (hashing, encryption).
 - `libdb`: Database abstraction (PostgreSQL).
 - `libkv`: Key-Value store abstraction (TiKV, Valkey/Redis, NATS), will be used for distributed cache.
 - `libollama`: library for interacting with Ollama features not exposed via ollama-API (tokenization).
 - `libroutine`: Goroutine management utilities, like circuit breaker.
 - `libtestenv`: Utilities for setting up testing environments for integration tests.

## API tests (`apitests`)

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

Workers are responsible for processing Jobs asynchronously, such as parsing and indexing documents, or generating embeddings for text data. They gain Jobs by polling the dispatchapi endpoints and marking them as done, when the results are ingested into the core.
