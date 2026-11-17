# Run a native model with Contenox Runtime

Modeld runs llama.cpp and OpenVINO models on the machine that holds the weights. Runtime connects to a shared Modeld owner and requests inference. The owner schedules native workers and keeps compatible allocations resident between requests. The CLI, HTTP server and ACP clients can use the same owner.

## How Runtime and Modeld communicate

Runtime and Modeld speak protocol **4** through the gRPC service `contenox.transport.v1.Compute`. Its hand-written bindings encode requests and responses as JSON using the `contenoxtransportjson` content subtype. ACP sits between an editor and Runtime. Runtime uses the same Modeld client whether a request originated in an editor, the CLI or the HTTP API.

1. For a local backend, Runtime discovers the owner through the lease in the data root and can start one on demand. For a remote backend, it dials the configured endpoint. Runtime checks the owner's protocol and instance identity. Fenced requests carry the expected owner instance so a replacement can reject a stale client.
2. Runtime queries the owner's inventory and model metadata. The owner controls admission, claims device capacity, and starts or reuses a native worker for the requested model and role. It reports the capabilities and context window the hardware can actually serve.
3. For generation, Runtime opens a session, prepares its prompt with `EnsurePrefix` and `PrefillSuffix`, and consumes the server-streamed `Decode` response. The stream can carry text, thinking, tool calls, and token usage. The protocol also supports embeddings, session snapshots and restore. Closing the session releases its handle. An idle worker can stay loaded for another request.
4. The same service exposes inventory, worker inspection, admission and model administration. A client can push model data to a node with the client-streamed `PushModel` RPC.

Editors can use the Runtime ACP extension `_contenox/inference` on a fresh installation to inspect the catalog, connect a provider, review an automatic setup plan, verify role assignments and apply them. Long-running operations send `_contenox/operation` progress notifications. The `_contenox/workers/*` methods inspect and manage workers, allocation and admission. An editor requests a completion with `_contenox/autocomplete` and a versioned edit prediction with `_contenox/next_edit`. The request and response types are defined in [the ACP protocol schemas](../proto/contenox/v1/).

Editors using the standalone JSON-lines interface can run `contenox-runtime autocomplete --stdio`. Each line carries a request ID, file path, prefix and suffix. Match responses by ID because requests can finish out of order. The command's help includes a complete wire example.

## Prepare the worker

For a source build, follow [Building the native inference worker](development/modeld-build.md). Set `CONTENOX_MODELD_BIN` to the packaged launcher, including its side-by-side native libraries. To install from a Modeld release index instead, set `CONTENOX_MODELD_BASE_URL` to the HTTP directory containing `index.json` and run:

```sh
contenox-runtime modeld install --backend llama
contenox-runtime modeld status --json
```

Use `openvino` for an OpenVINO installation. Runtime starts the local owner when it needs inference. Set `CONTENOX_DATA_ROOT` to select a directory for the worker lease, model library and owner state. Its default is `~/.contenox`.

## Select and verify a model

```sh
contenox-runtime auto --dry-run
contenox-runtime auto
```

The preview reports the detected hardware, proposed model and estimated hot context. Applying the plan acquires the model, checks native inference and tool calling, and saves the verified selection. Actual context capacity comes from the worker when it opens the model.

`auto` budgets for embedding and autocomplete before selecting the primary model. It checks that the primary model has evidence of multi-step tool use and that its weights and KV cache fit in the remaining memory. If the three roles cannot fit together, the preview reports the limiting role or memory requirement. You can still select a single model manually. `--refresh` asks the planner to select again. `CONTENOX_LLAMA_KV_CACHE_TYPE` controls the llama KV format used in the estimate and in the worker. Downloads support `HF_ENDPOINT` for a mirror and `HF_TOKEN` for authenticated model access.

To choose a model yourself, use its exact name from the registry:

```sh
contenox-runtime model registry-list
contenox-runtime model pull qwen2.5-coder-7b
contenox-runtime list
contenox-runtime backend add modeld --type modeld --url local
contenox-runtime config set default-provider modeld
contenox-runtime config set default-model qwen2.5-coder-7b
```

## Inspect the running system

| Command | What it reports or changes |
| --- | --- |
| `contenox-runtime list` | Model artifacts installed on this machine |
| `contenox-runtime show <model>` | Worker-resolved capabilities, device and context capacity |
| `contenox-runtime ps --json` | Resident native worker state |
| `contenox-runtime stop <model>` | Unloads the selected idle model |
| `contenox-runtime modeld status --json` | Local launcher, owner endpoint and reachability |
| `contenox-runtime role on <role>` / `role off <role>` | Enable or disable a verified optional role such as embedding |

A remote owner serves the same versioned gRPC interface. On the model host, launch `modeld owner --listen <private-address>:<port>` with its model data root. Register that host and port as the Modeld backend URL on the client. The native protocol has no authentication or transport encryption. Use loopback or a trusted private connection such as an SSH tunnel.

## Reuse a warm session

When a backend supports snapshots, Modeld attempts to save native state before unloading an idle worker, switching models or shutting down gracefully. A later session can restore that state when the model assets, adapters, template, backend and context configuration match. An incompatible or corrupt snapshot triggers a fresh session.

Snapshots include prompt-derived data. Modeld keeps them in the worker user's private `modeld-snapshots/<backend>/slot-v1` directory beneath the data root. Set `CONTENOX_WARM_SNAPSHOT_DIR` in the worker environment to move the cache, or `CONTENOX_WARM_SNAPSHOT_DISABLE=1` to disable automatic snapshots.

For a saved local chat, Runtime proves access to the same data root when reconnecting. This lets Modeld recognize that chat's snapshot across CLI invocations. Other chats get their own snapshot identities. Live session handles remain tied to the connection that opened them.
