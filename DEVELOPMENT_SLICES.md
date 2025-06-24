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

### 📅 June 2025 – Packaging a Chat Application

#### Goals:
- Package a chat application with persona support
- Add user registration and task execution commands
- Begin observability and release infrastructure

#### In Progress / Completed:
- [x] **RAG-Enhanced Chat Interface**
- [x] **Chat with Task Command Execution Support**
- [x] **Registration Route for Persona Chat Users**
- [ ] **Release Processes**
- [ ] **Observability Integration**
- [ ] **Release Infrastructure Setup**
- [ ] **API Rate Limiting Middleware**
- [ ] **Package a Persona-Chat Application**
- [x] **Telegram bot integrations**
- [x] **Simple OpenAI SDK compatible chat endpoint**
- [x] **vLLM Integration**

---

### 📅 Future Slices (Wishlist)

#### Potential Focus Areas:
- Multi-user collaboration via shared chat sessions
- Pull based LLM Provider implementation
- Persiting Tasks + UI-Tasks Builder
- Slack bot integrations
- Voice interface integration
- Audit logging and compliance tooling
- Exportable conversation transcripts
- Model fine-tuning management dashboard
- Implement queue based model provider
- Testing with openAI compatible frontends for chat applications
- Multiple Telegram-Bot integrations via UI
