## 📄 About DEVELOPMENT_SLICES.md

> This document tracks development efforts by slice/month, showing what was completed, what is in progress, and planned upcoming features.

---

### 📅 April 2025 – Semantic Search

#### Goals:
- Enable semantic search over embedded documents
- Improve backend pooling and model routing logic
- Migrate tokenizer into a standalone service
- Replace OpenSearch with Vald for vector search

#### Completed Features:
- [x] **UI-Search Page**: Developed to demo semantic search functionality.
- [x] **Backend Pooling**: Finalized implementation of backend pools/fleets, allowing grouping and assignment of models.
- [x] **Tokenizer Service Migration**: Tokenizer logic moved into its own microservice for better build performance and scalability.
- [x] **Document Ingestion Pipeline**:
  - Python workers now parse and process documents from the filestore.
  - Embeddings are generated and ingested into **Vald**.
  - Replaced OpenSearch with **Vald** due to better gRPC support, Go integration, and faster dev setup.
- [x] **LLM Resolver Enhancements**:
  - Improved scoring system for selecting optimal backend/model.
  - Routing policies now consider load, capabilities, and availability.
- [x] **Fix wiring**: Ensured previously built components worked end-to-end.
- [x] **Testing & CI**: Fixed failing tests and set up basic Continuous Integration.

---

### 📅 May 2025 – Documents QA

#### Goals:
- Build a UI page for natural language document Q&A
- Prepare infrastructure for reusable prompt chains

#### Completed Features:
- [x] **Documents QA UI Page**: Allows users to ask questions and get answers based on relevant documents.
- [x] **Prompt Execution Service**: Created a service that executes prompts, used by workers to chunk text using semantic understanding.
- [x] **Prompt Chain Service**: Implemented to run sequences of prompts for QA and automation workflows.
- [x] **Filesystem Performance Improvements**: Optimized slow file renaming operations.
- [x] **OpenAPI Spec Review**: Reviewed endpoints and began planning for API documentation delivery (not yet feasible).
- [x] **Cleaning & Wiring**: Ensured all components were integrated and passing tests.

---

### 📅 June 2025 – Taskengine & core

#### Goals:
- Package a chat application with persona support
- Add user registration and task execution commands
- Begin observability and release infrastructure

#### In Progress / Completed:
- [x] **RAG-Enhanced Chat Interface**
- [x] **Chat with Task Command Execution Support**
- [x] **Registration Route for Persona Chat Users**
- [x] **OpenAI Driver Integration**
- [x] **Gemini Driver Integration**
- [x] **Release Processes**
- [x] **Release Infrastructure Setup**
- [x] **Telegram Bot Integrations**
- [x] **Simple OpenAI SDK-Compatible Chat Endpoint**
- [x] **vLLM Integration**

Notes:
- Formal release processes are not part of the MVP; they will be implemented in the re-architecture phase.
- Packaging the platform as an application for persona-based chat was moved to the next cycle.

---

### 📅 July 2025 – Building a demo application

> Continued development to support real-world task chains (e.g. GitHub PR moderator), including DSL/runtime updates.

- [ ] **Package a Persona-Chat Application**
- [x] **Basic Observability Integration & UI Dashboard**
- [x] **API Rate Limiting Middleware**
- [x] **Implement Chat Moderation**
- [ ] **GitHub PR Moderator**
- [ ] **Fix Task-Engine design**

---

### 📅 Future Slices (Wishlist)

#### Potential Focus Areas:
- Fix permission model
- Teardown the monolith store
- Improve backend architecture
- Multi-user collaboration via shared chat sessions
- Pull-based LLM provider implementation
- Persisting Tasks + UI Task Builder
- Slack bot integrations
- Voice interface integration
- Audit logging and compliance tooling UI dashboards
- Exportable conversation transcripts
- Model fine-tuning management dashboard
- Implement queue-based model provider
- Testing with OpenAI-compatible frontends for chat applications
- Multiple Telegram bot integrations via UI
- Adding user-defined frontend connectors
- Adding a way to upload tasks and attach them to connectors
- Sticky session routing policy
- Implement MCP compatibility so that MCP servers (MCP provides a consistent way for AI models to access and utilize external information) are detected by the task engine and usable as hooks
