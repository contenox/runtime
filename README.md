# cate

A modular platform for building context-aware agents, semantic search, and task automation — grounded in your data.

## 🚀 Project Vision

cate (cognitive AI/Agent transformation engine/environment) aims to become a platform for semantic search and user-defined AI agents that operate within specific contexts.

The project's vision is focused on delivering these core features:

- **Document Ingestion**: Upload PDFs, text files, or URLs to build a knowledge base.
- **Semantic Search**: Search for relevant information within the declared knowledge base.
- **Contextual Chat Sessions**: Ask questions and get answers grounded in your documents.
- **Task Handling**: Create templates for repetitive tasks and execute them with user input.
- **Triggers**: Define conditions that trigger actions based on semantic matches.
- **Steps**: For complex requests, let the agent split the request into a chain of prompts.

## 🔧 What's Under the Hood

cate combines several technologies to deliver its features:

- **Core Logic**: The main backend service and LLM Gateway, built in **Go**, provides the primary API and orchestration.
- **User Interface**: Dashboards and user interactions are handled by a **React** frontend.
- **Auxiliary Services**: Data processing tasks like document mining and indexing are handled by separate services written in **Python**.
- **Language Models**: Core AI capabilities rely on **Ollama** backends.
- **Security**:
  - Authentication is handled via **JWT tokens**.
  - A **Backend-for-Frontend (BFF)** pattern helps the UI securely manage token lifecycles.
  - User management and permissions utilize a **custom Access Control system**.
- **Deployment**:
  - The system is designed to run **containerized** (e.g., using Docker).
  - Users are expected to provide external dependencies like **PostgreSQL**, **Valkey**, and **Vald**.
  - A `docker-compose.yml` file is provided for convenience, but operators typically deploy the cate container image(s) directly and manage configuration externally.

## 📊 Current State (As of April 2025)

cate is in **active development**, while end-to-end features are still being refined and integrated, the following capabilities have established implementations:

* **Core Backend Services:** Modular Go services including llm-backend/model management (`backendservice`), chat session logic (`chatservice`), file handling (`fileservice`), user/access control (`userservice`, `accessservice`)
* **Persistence & State:** Storage logic (`core/serverops/store`) is implemented for core entities using PostgreSQL, **Vald (for vector storage)**, and Valkey (via `libkv`). State synchronization for Ollama backends is functional. *(Corrected path, Replaced OpenSearch with Vald)*
* **Foundational Libraries:** Core libraries (`libs/`) provide reusable components for DB/KV access, authentication (`libauth`), messaging (`libbus`), crypto (`libcipher`) and Ollama interaction (`libollama`).
* **API & UI Structure:** The React frontend (`frontend/`) includes routing, core pages for chat, admin (users, backends) and JWT authentication flow via a BFF is implemented.
* **Basic Operations:** The system is containerized (`Dockerfile`, `compose.yaml`), includes build/run processes (`Makefile`).

## 🛠️ Last Development Slice

-> Semantic Search
* [x] **UI-Search:** Develop a UI-Search page to demo semantic search.

Steps needed:
    * [x] **Backend Pooling** Finalizing the implementation for grouping backends manageable pools/fleets assigning models to them.
    * [x] **Tokenizer Service Migration** Moving tokenizer logic into a dedicated service to optimize core service build times and resource usage.
    * [x] **Document Ingestion Pipeline:** Building the initial RAG pipeline, with Python workers, to parse and process documents from the filestore and ingest the embeddings into **vald**. *(Replaced opensearch with vald)*
    * [x] **LLM Resolver:** Improving the logic (`llmresolver`) for selecting the optimal backend instance and model for requests, via a scoring system and routing policies.
    * [x] **Fixing wiring:** Ensuring previously built features are fully integrated and functional E2E.
    * [x] **Cleaning:** Fix failing tests and get a basic CI running.

---
Notes from the devslice:
- *Rationale for Vald:* Vald was selected over OpenSearch due to its specialized focus on high-performance/scalable vector search, resulting in simpler integration as a core engine component compared to OpenSearch's broad feature set. Additional benefits included its suitable gRPC API, improved type-safety within the Go ecosystem, and faster spin-up times for development environments.

## 🛠️ Current Development Slice

-> Documents QA
* [ ] **Build UI-Documents QA Page:** This is about a UI page where a user can ask a question in a natural language format and gets a response with the most relevant documents and maybe a brief summary why.

Steps needed:
    * [ ] **Expose Tokenizer Service to workers:** Implement a tokenizer service that can be used by workers to tokenize text.
    * [ ] **Expose Prompt via a Service:** Create a service that can be used to execute a prompt, for workers to chunk text using semantic understanding.
    * [ ] **Prompt Chains** Implement a prompt chain service that can be used to execute a sequence of prompts, for the QA page.
    * [ ] **Improve Filesystem Performance:** Renaming files is currently slow, this is a nice to have task for this slice.
    * [ ] **OpenAPI spec:** Review the endpoints and start establishing how to document APIs and how to serve the specifications.
    * [ ] **Cleaning & wiring:** Ensure everything works as expected and tests are passing.


## 🗺️ Roadmap (Near-Term Focus)

Development is dynamic, but the immediate priorities are centered on bringing the core features online:

1. **Semantic Search:** Implementing search capabilities over ingested documents using vector embeddings.
2. **Contextual Chat (RAG):** Enhancing chat sessions to utilize retrieved document context for grounded responses.
3. **Task Handling (Templates):** Building the UI and backend logic for defining and executing simple user task templates.

## ⚙️ Starting the Development Environment

### Prepare the Environment
  * Copy and edit `.env-example` into a new `.env` file with the proper configuration.
  * Install prerequisites: Docker, Docker Compose, Yarn, and Go.
### Build and Run the Backend Services
  * Run the following to build Docker images and start all services:
    ```bash
    make run
    ```
  * Use `make logs` to tail the backend logs if needed.
### Run the Frontend & UI
  * The backend includes a proxy (Backend-for-Frontend/BFF) to handle UI requests and authentication cookies correctly.
  * Start the UI development workflow, which builds UI components and runs the Vite dev server:
    ```bash
    make ui-run
    ```
  * Once Vite is running (you'll see its output in the terminal, often mentioning a port like 5173), **access the application in your browser via the main backend URL** (e.g., `http://localhost:8080` or as configured in your `.env` file or docker-compose port mappings).
  * **Important:** Do *not* use the local URL Vite might display (like `localhost:5173`), as login and other authenticated features will not work correctly through it due to how browser cookies are handled. Always access the UI through the backend's address during development. NOTE: Register
  as `admin@admin.com` for system privileges.

### API Tests Setup & Execution
  * Initialize the Python virtual environment and install API test dependencies:
    ```bash
    make api-init
    ```
  * Run your API tests via:
    ```bash
    make api-test
    ```
