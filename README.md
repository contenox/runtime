# contenox/runtime-mvp

> **Build agent-based applications with LLMs** - An open runtime for chat-driven automation, RAG systems, and copilot experiences

This MVP demonstrates the vision for building **production-ready agent systems**:
- Execute complex workflows through natural language
- Create custom automations with task chains
- Deploy secure, extensible copilot experiences
- Own the entire stack - no black-box dependencies

---

## 🚀 Core Capabilities

| Feature | What It Enables |
|---------|----------------|
| **🧠 Task Chains** | Stateful workflows with branching logic and hooks |
| **🔍 RAG Engine** | Q&A over documents with Vald vector search |
| **🤖 Bot Framework** | Create GitHub/TG bots that execute tasks |
| **🪝 Extensible Hooks** | Connect APIs, databases, and custom logic |
| **🔌 Multi-LLM Gateway** | Unified interface for Ollama/vLLM/OpenAI/Gemini |
| **💬 Chat Commands** | Execute tasks via `/search`, `/help` etc. |

---

## 🧠 In short

The primary aim of this MVP is to refine the DSL and core capabilities, while ensuring the system can be reliably operated — including model lifecycles, observability, usage analytics, security, and regulatory compliance.

A secondary goal is to showcase the performance and scalability of the underlying infrastructure and services.

> **Note**: ⚠️ The codebase is under active development and may change frequently until the first stable release.
See [DEVELOPMENT_SLICES.md](DEVELOPMENT_SLICES.md) for the Progress roadmap.

---

## ⚙️ Architecture

| Layer | Technology |
|-------|------------|
| Backend | Go |
| Frontend | React + TypeScript |
| LLMs | Ollama, vLLM, OpenAI, Gemini |
| Vector DB | Vald |
| Database | PostgreSQL |
| Auth | JWT, custom access control |
| Deployment | Docker |


## 🔌 Technical Highlights
- **Task Engine**: Go-based state machine for workflow execution
- **Vector Pipeline**: Document parsing → embedding → indexing → retrieval
- **Auth**: JWT with granular access policies
- **Frontend**: React/TS admin UI for configuration
- **Bots**: Connect to GitHub, Telegram, Slack


### Tooling and Structure
- **Go-based Core**: Handles orchestration, business logic, and integrations.
- **React Frontend**: Lightweight UI for chat, admin, and configuration.
- **Python Workers**: Asynchronous processing of jobs like document indexing.
- **API Tests**: Python-based tests for verifying backend functionality.
- **Docker Setup**: Easy containerization for local development and demo deployments.


See [STRUCTURE.md](STRUCTURE.md) for the codebase architecture

---

## Tasks-Chains

Task Chains are declarative, configurations for the state machine to perform composed of modular steps (tasks) with branching logic and hooks. These chains can power chat interactions, automation flows, or agent reasoning.
Create tasks-chains on runtime via the admin panel and assign them to frontends and bots.

A working example:

### Example
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
    prompt_template: "Classify the input as safe (0) or spam (10) respond with an numeric value between 0 and 10. Input: {{.input}}"
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
    prompt_template: "Rate how likely it is that the answer requires access to this internal information respond with an numeric value between (0) not likely and (10) highly likely. Input {{.input}}"
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
    description: Search knowledge base
    type: hook
    hook:
      type: search_knowledge
    transition:
      branches:
        - operator: default
          goto: append_search_results

  - id: append_search_results
    type: noop
    prompt_template: "here are the found search results for the requested document recap them for the user: {{.search_knowledge}}"
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
    prompt_template: "respond to the user that request was rejected because the input was flagged: {{.input}}"
    input_var: input
    transition:
      branches:
        - operator: default
          goto: raise_error

  - id: request_failed
    description: Reject the request
    type: raw_string
    prompt_template: "respond to the user that classification of the request failed for context the exact input: {{.input}}"
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
    system_instruction: "You're a helpful assistant in the contenox system. Respond helpfully and mention available commands (/help, /echo, /search) when appropriate. Keep conversation friendly."
    execute_config:
      models: []
      providers: []
    transition:
      branches:
        - operator: default
          goto: end
```

## 🛠️ Development

Getting started is simple.
Create a .env from .env-example.
& Run:

```bash
make run       # Start all services
make ui-run    # Run frontend dev server
```

Access the UI at `http://localhost:8081` and register as `admin@admin.com`.
(Note the frontend is proxied through the core-backend server, use the backend's port not Vite's port)
