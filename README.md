# contenox/runtime-mvp

> **Build agent-based applications with LLMs** — An open runtime for chat-driven automation, RAG systems, and copilot experiences.

This MVP demonstrates the vision for building **production-ready agent systems**:

* Execute complex workflows through natural language
* Create custom automations using Task Chains
* Deploy secure, extensible copilot experiences
* Own the entire stack — no black-box dependencies

---

## 🚀 Core Capabilities

| Feature                  | What It Enables                                           |
| ------------------------ | --------------------------------------------------------- |
| **🧠 Task Chains**       | Stateful workflows with branching logic and hooks         |
| **🔍 RAG Engine**        | Q\&A over documents using Vald vector search              |
| **🤖 Bot Framework**     | Create GitHub/Telegram bots that execute tasks            |
| **🪝 Extensible Hooks**  | Connect APIs, databases, and custom logic                 |
| **🔌 Multi-LLM Gateway** | Unified interface for Ollama, vLLM, OpenAI, Gemini        |
| **💬 Chat Commands**     | Trigger tasks with `/search`, `/help`, and other commands |

---

## 🧠 In Short

The **primary goal** of this MVP is to refine the internal DSL and core capabilities, while ensuring the system is production-grade across areas like model lifecycles, observability, usage analytics, security, and regulatory compliance.

A **secondary goal** is to demonstrate the performance and scalability of the infrastructure and services powering the runtime.

> ⚠️ **Note**: The codebase is under active development and may change frequently until the first stable release.
> See [DEVELOPMENT\_SLICES.md](DEVELOPMENT_SLICES.md) for the progress roadmap.

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

* **Task Engine**: Go-based state machine for workflow execution
* **Vector Pipeline**: Document parsing → embedding → indexing → retrieval
* **Authentication**: JWT with fine-grained access control
* **Frontend**: React/TypeScript UI for admin and configuration
* **Bot Integrations**: GitHub (WiP), Telegram, Slack (WiP) support

---

### 🧰 Tooling and Structure

* **Go Core**: Handles orchestration, business logic, and integrations
* **React Frontend**: Lightweight admin and chat interface
* **Python Workers**: Async jobs for document processing and indexing
* **API Tests**: Python-based test suite for backend validation
* **Docker Setup**: Local development and deployment via containers

See [STRUCTURE.md](STRUCTURE.md) for a breakdown of the codebase architecture.

---

## 🧩 Task Chains

Task Chains are declarative, state-machine configurations made up of modular steps (called *tasks*), which support variables, branching logic, and pluggable hooks. These chains power chat flows, automations, and agent reasoning.

Chains can be created at runtime via the admin UI and assigned to frontends or bots.

### 🧪 Example Task Chain

```yaml
id: chat_chain
description: Standard chat processing pipeline with hooks
debug: true
tasks:
  - id: mux_input
    description: Check for commands like /echo
    type: parse_transition
    transition:
      branches:
        - operator: default
          goto: moderate
        - operator: equals
          when: echo
          goto: echo_message
        - operator: equals
          when: help
          goto: print_help_message
        - operator: equals
          when: search
          goto: search_knowledge

  - id: moderate
    description: Moderate the input
    type: parse_number
    prompt_template: "Classify the input as safe (0) or spam (10). Respond with a numeric value between 0 and 10. Input: {{.input}}"
    input_var: input
    transition:
      branches:
        - operator: ">"
          when: "4"
          goto: reject_request
        - operator: default
          goto: do_we_need_context
      on_failure: request_failed

  - id: do_we_need_context
    description: Add context to the conversation
    type: raw_string
    prompt_template: "Rate how likely it is that the answer requires access to internal information. Respond with a value between 0 (not likely) and 10 (highly likely). Input: {{.input}}"
    input_var: input
    transition:
      branches:
        - operator: default
          goto: swap_to_input
        - operator: ">"
          when: "4"
          goto: search_knowledge

  - id: swap_to_input
    type: noop
    input_var: input
    transition:
      branches:
        - operator: default
          goto: execute_model_on_messages
          alert_on_match: Test Alert

  - id: echo_message
    description: Echo the message
    type: hook
    hook:
      type: echo
    transition:
      branches:
        - operator: default
          goto: end

  - id: search_knowledge
    description: Search the knowledge base
    type: hook
    hook:
      type: search_knowledge
    transition:
      branches:
        - operator: default
          goto: append_search_results

  - id: append_search_results
    type: noop
    prompt_template: "Here are the search results: {{.search_knowledge}}"
    compose:
      with_var: input
      strategy: append_string_to_chat_history
    transition:
      branches:
        - operator: default
          goto: execute_model_on_messages

  - id: print_help_message
    description: Display help message
    type: hook
    hook:
      type: print
      args:
        message: |
          Available commands:
          /echo <text>
          /help
          /search <query>
    transition:
      branches:
        - operator: default
          goto: end

  - id: reject_request
    description: Reject the request
    type: raw_string
    prompt_template: "Your request was rejected due to input moderation: {{.input}}"
    input_var: input
    transition:
      branches:
        - operator: default
          goto: raise_error

  - id: request_failed
    description: Fallback if moderation fails
    type: raw_string
    prompt_template: "Classification of the request failed. Input: {{.input}}"
    input_var: input
    transition:
      branches:
        - operator: default
          goto: raise_error

  - id: raise_error
    description: Raise an error
    type: raise_error
    prompt_template: "Error processing: {{.input}}"
    input_var: input
    transition:
      branches:
        - operator: default
          goto: end

  - id: execute_model_on_messages
    description: Run inference using selected LLM
    type: model_execution
    system_instruction: "You're a helpful assistant in the Contenox system. Respond helpfully and mention available commands (/help, /echo, /search) when appropriate. Keep the conversation friendly."
    execute_config:
      models: []
      providers: []
    transition:
      branches:
        - operator: default
          goto: end
```

---

## 🛠️ Development

Getting started is simple:

1. Copy `.env-example` to `.env`
2. Run the services:

```bash
make run       # Start backend, workers, and vector search
make ui-run    # Start frontend dev server
```

Access the UI at [http://localhost:8081](http://localhost:8081) and register as `admin@admin.com`.

> **Note**: The frontend is **proxied through the backend**. Use the backend's port (8081), not Vite's default port.
