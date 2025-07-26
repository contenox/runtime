# contenox/runtime-mvp

> **Build context-aware AI systems with LLMs** — An open runtime for Autonomous Agents (Bots) and Co-Pilots (Frontends) with shared behavior logic.

This MVP demonstrates the vision for building **agent systems**:

* Execute chained sequences of operations through natural language
+ Route events to contextual agents with dynamic behavior loading
* Create custom automations using Task Chains
* Deploy secure, extensible copilot experiences
* Own the entire stack — no black-box dependencies

---

## 🚀 Core Capabilities

| Feature                  | What It Enables                                           |
| ------------------------ | --------------------------------------------------------- |
| **🧠 Task Chains**       | Define agent personalities via DSL; load behaviors dynamically at runtime          |
| **🔍 RAG Engine**        | Semantic search + Q&A over documents, powered by Vald for scalable vector search              |
| **🤖 Agent Dispatch**    | Process external events through a multi-stage job system (sync → process → respond) |
| **🪝 Extensible Hooks**  | Connect APIs, databases, and custom logic with contextual parameters |
| **🔁 LLM Orchestration** | Seamlessly switch between OpenAI, vLLM, Gemini, and more via pluggable backends and intelligent routing |
| **💬 Chat Commands**     | Trigger tasks with `/search`, `/help`, and other commands |

---

## 🧠 In Short

The **primary goal** of this MVP is to demonstrate how the same state-machine can power both user-facing Co-Pilots and Autonomous Agents, while ensuring the system is production-grade across areas like observability, security, and regulatory compliance.

A **secondary goal** is to showcase the performance and scalability of the agent dispatch infrastructure.

> ⚠️ **Note**: The codebase is under active development and may change frequently until the first stable release. Not all features are poli yet.
> See [DEVELOPMENT_SLICES.md](DEVELOPMENT_SLICES.md) for the progress roadmap.

---

## 🌐 Interaction Models

contenox supports two primary interaction patterns

### 👤 Co-Pilots (Frontends)
User-facing interfaces where humans interact directly with task chains:
- Telegram chat interface
- OpenAI API-compatible endpoints
- Custom chat UIs
- CLI interfaces

*Co-Pilots maintain conversation history with users and respond directly to their inputs.*

### 🤖 Autonomous Agents (Bots)
Task chains that operate autonomously on external systems:
- GitHub PR comment processors
- Content moderation systems
- Social media managers
- Internal workflow automations

*Autonomous Agents detect events, process them through task chains, and take actions on external systems - all while maintaining context-specific conversation histories.*

Both patterns use the same DSL and execution engine, but differ in how they're triggered and where they operate.

---

## ⚙️ Architecture

| Layer      | Technology                   |
| ---------- | ---------------------------- |
| Backend    | Go                           |
| Frontend   | React + TypeScript           |
| LLMs       | Ollama, vLLM, OpenAI, Gemini |
| Vector DB  | Vald                         |
| Database   | PostgreSQL                   |
| Auth       | JWT, custom access control   |
| Deployment | Docker                       |

---

## 🔌 Technical Highlights

* **Agent Dispatch System**: Multi-stage job processing for event-driven agent execution
* **Task Engine**: Go-based state machine for execution of sequences
* **Vector Pipeline**: Document parsing → embedding → indexing → retrieval
* **Authentication**: JWT with fine-grained access control
* **Frontend**: React/TypeScript UI for admin and configuration
* **Bot Integrations**: GitHub (fully implemented), Telegram, Slack (in progress)

---

### 🧰 Tooling and Structure

* **Go Core**: Handles orchestration, business logic, and integrations
* **React Frontend**: Lightweight admin and chat interface
* **Python Workers**: Async jobs for document processing and indexing
* **API Tests**: Python-based test suite for backend validation
* **Docker Setup**: Local development and deployment via containers

See [STRUCTURE.md](STRUCTURE.md) for a breakdown of the codebase architecture.

---

## 🧩 Task Chains & Contextual Execution

Task Chains are declarative, state-machine configurations made up of modular steps (called *tasks*), which support variables, branching logic, and pluggable hooks. These chains power both Co-Pilots and Autonomous Agents.

Chains can be created at runtime via the admin UI and assigned to frontends or bots. Crucially, they support **contextual execution** - when triggered by external events (like GitHub comments), the system dynamically injects context parameters (such as `subject_id: repo:pr`) into the chain.

This enables the same behavior definition to work across multiple contexts while maintaining separate conversation histories.

### 🧪 Example Task Chain with Context

_illustrative example_

```yaml
id: github_comment_chain
description: Process GitHub comments with contextual awareness
tasks:
  - id: moderate
    description: Moderate the input
    type: parse_number
    prompt_template: "Classify input safety (0=safe, 10=spam): {{.input}}"
    input_var: input
    transition:
      branches:
        - operator: ">"
          when: "4"
          goto: reject_request
        - operator: default
          goto: execute_chat_model

  - id: execute_chat_model
    description: Run inference using selected LLM
    type: model_execution
    system_instruction: "You're a helpful GitHub assistant. Reference PR context when relevant."
    execute_config:
      models:
        - gemini-2.5-flash
      providers:
        - gemini
    input_var: input
    transition:
      branches:
        - operator: default
          goto: end
```

---

## ⚡ How Agent Dispatch Works

contenox processes external events through a multi-stage job system:

1. **Event Detection** (e.g., GitHub comment):
   ```go
   // Worker detects new GitHub comments
   comments, err := w.githubService.ListComments(ctx, repoID, prNumber, lastSync)
   ```

2. **Job Creation**:
   ```go
   // Creates LLM processing job for each new comment
   job := &store.Job{
     ID:        uuid.NewString(),
     TaskType:  JobTypeGitHubProcessCommentLLM,
     Subject:   fmt.Sprintf("%s:%d", repoID, prNumber),
     Payload:   payload,
   }
   ```

3. **Agent Execution**:
   ```go
   // Processor finds matching bot by job type
   bots, err := storeInstance.ListBotsByJobType(ctx, job.TaskType)

   // Loads the bot's task chain
   chain, err := tasksrecipes.GetChainDefinition(ctx, p.db.WithoutTransaction(), bot.TaskChainID)

   // Executes the chain
   result, stacktrace, err := p.env.ExecEnv(ctx, chain, payload.Content, taskengine.DataTypeString)
   ```

This pattern enables contextual agent execution where the same behavior definition works across different contexts (e.g., multiple GitHub PRs), with each context maintaining its own conversation history.

---

## 🛠️ Development

Getting started is simple:

1. Copy `.env-example` to `.env`
2. Run the services:

```bash
make run        # Start backend, workers, and vector DB
make ui-run     # Start React frontend (proxied via backend)
```

Access the UI at [http://localhost:8081](http://localhost:8081) and register as `admin@admin.com`.

> **Note**: The frontend is **proxied through the backend** on port `8081`. Do not use Vite's default port.
