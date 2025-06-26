# contenox/runtime-MVP

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

## 🔌 Architecture Overview

This MVP combines several technologies and architectural patterns to deliver these capabilities.

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

The architecture is intentionally modular, allowing for future separation into microservices and standalone tools.

---

## 🛠️ Developer Experience

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

Access the UI at `http://localhost:8080` and register as `admin@admin.com`.

---

## 🤝 Contributing & Exploring

We welcome explorers — especially those interested in:

- Building agent-based applications
- Extending task chains and hook recipes
- Improving semantic search pipelines
- Designing better UI experiences for AI workflows

Whether you're trying out a demo deployment, joining a challenge, or just digging into the code, this MVP is your entry point to understanding what contenox aims to become.

---

## 📄 License

Apache 2.0
