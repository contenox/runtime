# contenox/runtime-mvp

> *A runtime engine to power agent-based applications, semantic search systems, and LLM-powered automation*

This is the **Minimum Viable Product (MVP)** of `contenox-runtime` — the first working version of a system designed to build and operate agent-based applications, conversational interfaces, and knowledge-grounded automation.

It serves as both a **technical foundation** and a **vision prototype**, showing how future versions can enable systems with language models to drive workflows, decisions, and interactions.

---

## 🧠 What It Does

At its core, this MVP demonstrates:

- **Conversational UIs**: Replace buttons with chat — slash commands (`/echo`, `/search_knowledge`) trigger actions directly from natural language input.
- **RAG-Powered Search & QA**: Ask questions and get answers rooted in internal documents and knowledge bases.
- **Prompt Chain Automation**: Define repeatable, multi-step tasks using chains of prompts, conditions, and hooks — enabling transparent, configurable agentic behavior.
- **Stateful Chat Sessions**: Maintain memory across conversations with role-based message history.
- **Command-Driven Actions**: Execute powerful operations from within chat.

In short: The aim of this MVP is to prove that you can **chat with data**, **automate tasks via logic**, and **extend capabilities through hooks** — all in one integrated system exposable via any conversational or natural language based frontend like Telegram, without lock-in into external black-box Model Provider-Services, while also ensuring everything you need reliably operate your own agent-based applications, from model lifecycles, observability, usage analytics, security, and regulatory compliance.

Secondary aim of the MVP is to demonstrate the capacity and performance of the system of the underlying infrastructure and services.

---

## 🔌 Architecture

| Layer | Technology |
|-------|------------|
| Backend | Go |
| Frontend | React + TypeScript |
| LLMs | Ollama, vLLM, OpenAI, Gemini |
| Vector DB | Vald |
| Database | PostgreSQL |
| Auth | JWT, custom access control |
| Deployment | Docker |

Key components include:

- **Task Engine**: Configurable chain engine supporting branching, retries, and model routing.
- **Hook System**: Extensible side-effect execution (e.g., send email, call API).
- **RAG Pipeline**: Document parsing → embedding → vector storage → retrieval.
- **Security Model**: JWT tokens, BFF pattern, access control.

---

## Example

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

This project includes tooling and structure to help developers explore and extend the system.

- **Go-based Core**: Handles orchestration, business logic, and integrations.
- **React Frontend**: Lightweight UI for chat, admin, and configuration.
- **Python Workers**: Asynchronous processing of jobs like document indexing.
- **API Tests**: Python-based tests for verifying backend functionality.
- **Docker Setup**: Easy containerization for local development and demo deployments.

Getting started is simple:
```bash
make run       # Start all services
make ui-run    # Run frontend dev server
```

Access the UI at `http://localhost:8081` and register as `admin@admin.com`.

---
