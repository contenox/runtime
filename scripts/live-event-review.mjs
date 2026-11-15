import assert from "node:assert/strict";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

const url = new URL(process.argv[2]);
assert.ok(
  ["127.0.0.1", "localhost"].includes(url.hostname),
  "Use a local test instance",
);
const root = await mkdtemp(join(tmpdir(), "contenox-live-review-"));
const rpcTimeout = 180_000;
async function api(path, method = "GET", body) {
  const res = await fetch(new URL("/api/" + path, url), {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  assert.ok(
    res.ok,
    method +
      " " +
      path +
      " failed: " +
      res.status +
      " " +
      (!res.ok ? await res.text() : ""),
  );
  return res.json();
}
await writeFile(join(root, "add.js"), "export const add = (a,b) => a-b;\n");
await api("workspace/roots", "POST", { path: root, name: "Live event review" });
const wsUrl = new URL("/acp", url);
wsUrl.protocol = "ws:";
const ws = new WebSocket(wsUrl);
const pending = new Map();
const calls = new Set();
let serial = 0;
function rpc(method, params) {
  const id = ++serial;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error("Timed out: " + method));
    }, rpcTimeout);
    pending.set(id, (m) => {
      clearTimeout(timer);
      m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result);
    });
    ws.send(JSON.stringify({ jsonrpc: "2.0", method, id, params }));
  });
}
ws.onmessage = ({ data }) => {
  const m = JSON.parse(data);
  if (
    m.method === "session/update" &&
    m.params.update.sessionUpdate === "tool_call"
  )
    calls.add(m.params.update.title);
  if (m.method === "session/request_permission") {
    const meta = m.params._meta;
    const grant =
      meta?.toolsName === "events" ||
      (meta?.toolsName === "local_fs" &&
        ["read_file", "write_file", "list_files"].includes(meta.toolName));
    const choice = m.params.options.find(
      (o) => o.kind === (grant ? "allow_once" : "reject_once"),
    );
    ws.send(
      JSON.stringify({
        jsonrpc: "2.0",
        id: m.id,
        result: { outcome: { outcome: "selected", optionId: choice.optionId } },
      }),
    );
  } else if (!m.method && pending.has(m.id)) {
    pending.get(m.id)(m);
    pending.delete(m.id);
  }
};
const query = "?root=" + encodeURIComponent(root);
const session = async () =>
  (await rpc("session/new", { cwd: root, mcpServers: [] })).sessionId;
const ask = async (sessionId, text) =>
  assert.equal(
    (
      await rpc("session/prompt", {
        sessionId,
        prompt: [{ type: "text", text }],
      })
    ).stopReason,
    "end_turn",
  );
async function until(label, read) {
  const deadline = Date.now() + rpcTimeout;
  do {
    const value = await read();
    if (value) return value;
    await delay(500);
  } while (Date.now() < deadline);
  throw new Error("Timed out: " + label);
}
async function verifySchedule() {
  const clock = await session();
  await ask(clock, "Use subscription_create to subscribe this chat to clock.review, target session, continue, permission set default. Set the saved prompt to: Call the events.event_fire tool now with topic clock.confirmed and payload {origin: clock.review}. Inspect the tool response and give the event number it returned. Register the subscription now and wait.");
  const rule = (await api("workspace/triggers" + query)).triggers.find(r => r.topicGlob === "clock.review");
  assert.ok(rule, "Scheduled agent must be subscribed before creating the schedule");
  const at = new Date(Date.now() + 5000).toISOString();
  await api("workspace/schedules" + query, "POST", { topic: "clock.review", at });
  const wake = await until("scheduled agent acknowledgement", async () => {
    const { events } = await api("workspace/events" + query + "&before=0&limit=100");
    const scheduled = events.find(e => e.subject === "clock.review" && e.source === "schedule");
    const confirmed = events.find(e => e.subject === "clock.confirmed" && e.source === "agent");
    const { firings } = await api("workspace/triggers/firings" + query);
    const firing = firings.find(f => f.triggerId === rule.id && f.status === "ok");
    return scheduled && confirmed && firing && { scheduled, confirmed, firing };
  });
  console.log(JSON.stringify(wake, null, 2));
}

async function verifyReview() {
  const author = await session(),
    reviewer = await session();
  await Promise.all([
    ask(
      author,
      "Create a file subscription named Author for REVIEW.md, target session, continue, permission set default. Set the saved prompt to: Read REVIEW.md and use event_fire to publish review.verified with the finding. Only register now.",
    ),
    ask(
      reviewer,
      "Create a topic subscription named Reviewer for review.requested, target session, continue, permission set default. Set the saved prompt to: Read add.js, review correctness and use write_file to save the finding in REVIEW.md. Only register now.",
    ),
  ]);
  const rules = (await api("workspace/triggers" + query)).triggers;
  assert.equal(
    rules.filter((r) => r.target === "session" && r.enabled).length,
    2,
  );
  assert.ok(rules.some(r => r.pathGlob === "REVIEW.md" && !r.topicGlob), "Author must watch REVIEW.md on disk");
  assert.ok(rules.some(r => r.topicGlob === "review.requested" && !r.pathGlob), "Reviewer must listen for review.requested");
  await ask(
    author,
    'Use event_list with subscribedOnly true and event_recent for the last 3 minutes. Then use event_fire to publish review.requested with payload {"file":"add.js"}.',
  );
  const evidence = await until(
    "review event and both session firings",
    async () => {
      const { events } = await api(
        "workspace/events" + query + "&before=0&limit=100",
      );
      const { firings } = await api("workspace/triggers/firings" + query);
      const completed = events.find(
        (e) => e.subject === "review.verified" && e.source === "agent",
      );
      assert.ok(
        firings.every(
          (f) => !["error", "refused", "parked"].includes(f.status),
        ),
        JSON.stringify(firings),
      );
      return (
        completed &&
        rules.every((r) =>
          firings.some((f) => f.triggerId === r.id && f.status === "ok"),
        ) && { completed, firings }
      );
    },
  );
  assert.match(
    await readFile(join(root, "REVIEW.md"), "utf8"),
    /a\s*-\s*b|subtr/i,
  );
  for (const name of ["event_list", "event_recent", "event_fire"])
    assert.ok(
      [...calls].some((title) => title.includes(name)),
      name + " missing from ACP tool calls",
    );
  console.log(JSON.stringify({ root, ...evidence }, null, 2));
}

try {
  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = reject;
  });
  await rpc("initialize", { protocolVersion: 1, clientCapabilities: {} });
  if (process.argv.includes("--schedule")) await verifySchedule();
  else await verifyReview();
} finally {
  ws.close();
  await api("workspace/roots?path=" + encodeURIComponent(root), "DELETE");
}
