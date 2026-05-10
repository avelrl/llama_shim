import { For, Match, Show, Switch, createMemo, createResource, createSignal } from "solid-js";
import { asNumber, asText, compactJSON, fetchJSON } from "./api";
import type { APIResult, CapabilityManifest, DebugTrace, DebugTraceList, EvidenceDetail, EvidenceList, EvidenceRecord } from "./types";

type View = "overview" | "capabilities" | "evidence" | "traces";
type StatusKind = "ready" | "warn" | "error" | "muted";

const rememberedToken = sessionStorage.getItem("llama_shim_operator_token") || "";

export default function App() {
  const [token, setToken] = createSignal(rememberedToken);
  const [rememberToken, setRememberToken] = createSignal(rememberedToken !== "");
  const [refresh, setRefresh] = createSignal(0);
  const [view, setView] = createSignal<View>("overview");
  const [selectedEvidenceID, setSelectedEvidenceID] = createSignal<string>("");
  const [selectedTraceID, setSelectedTraceID] = createSignal<string>("");

  const source = () => ({ token: token(), refresh: refresh() });
  const [health] = createResource(source, ({ token }) => fetchJSON<Record<string, string>>("/healthz", token));
  const [ready] = createResource(source, ({ token }) => fetchJSON<Record<string, string>>("/readyz", token));
  const [capabilities] = createResource(source, ({ token }) => fetchJSON<CapabilityManifest>("/debug/capabilities", token));
  const [evidence] = createResource(source, ({ token }) => fetchJSON<EvidenceList>("/debug/evidence", token));
  const [traces] = createResource(source, ({ token }) => fetchJSON<DebugTraceList>("/debug/traces?limit=25", token));
  const [evidenceDetail] = createResource(
    () => ({ token: token(), id: selectedEvidenceID(), refresh: refresh() }),
    ({ token, id }) => (id === "" ? Promise.resolve({ ok: true, status: 204, data: undefined }) : fetchJSON<EvidenceDetail>(`/debug/evidence/${encodeURIComponent(id)}`, token))
  );
  const [traceDetail] = createResource(
    () => ({ token: token(), id: selectedTraceID(), refresh: refresh() }),
    ({ token, id }) => (id === "" ? Promise.resolve({ ok: true, status: 204, data: undefined }) : fetchJSON<DebugTrace>(`/debug/traces/${encodeURIComponent(id)}`, token))
  );

  const authNeeded = createMemo(() => capabilities()?.status === 401 || evidence()?.status === 401 || traces()?.status === 401);
  const caps = createMemo(() => capabilities()?.data);
  const evidenceRows = createMemo(() => evidence()?.data?.data || []);
  const traceRows = createMemo(() => traces()?.data?.data || []);

  function updateToken(value: string) {
    setToken(value);
    if (rememberToken()) {
      sessionStorage.setItem("llama_shim_operator_token", value);
    }
  }

  function updateRemember(value: boolean) {
    setRememberToken(value);
    if (value) {
      sessionStorage.setItem("llama_shim_operator_token", token());
    } else {
      sessionStorage.removeItem("llama_shim_operator_token");
    }
  }

  return (
    <div class="app-shell">
      <aside class="sidebar">
        <div class="brand">
          <span class="brand-mark">ls</span>
          <div>
            <h1>llama_shim</h1>
            <p>operator</p>
          </div>
        </div>
        <nav class="nav">
          <button classList={{ active: view() === "overview" }} onClick={() => setView("overview")}>Overview</button>
          <button classList={{ active: view() === "capabilities" }} onClick={() => setView("capabilities")}>Capabilities</button>
          <button classList={{ active: view() === "evidence" }} onClick={() => setView("evidence")}>Evidence</button>
          <button classList={{ active: view() === "traces" }} onClick={() => setView("traces")}>Traces</button>
        </nav>
        <div class="side-note">
          <span>Trace redaction</span>
          <strong>{caps()?.runtime?.ops?.debug_traces?.redaction || "metadata only"}</strong>
        </div>
      </aside>

      <main class="workspace">
        <header class="topbar">
          <StatusPill label="health" kind={resultKind(health())} value={resultLabel(health(), "ok")} />
          <StatusPill label="ready" kind={resultKind(ready())} value={resultLabel(ready(), "ready")} />
          <StatusPill label="mode" kind="muted" value={caps()?.runtime?.responses_mode || "-"} />
          <StatusPill label="transport" kind="muted" value={caps()?.runtime?.responses_upstream_transport || "-"} />
          <button class="refresh" onClick={() => setRefresh((value) => value + 1)}>Refresh</button>
        </header>

        <Show when={authNeeded()}>
          <AuthPanel token={token()} remember={rememberToken()} onToken={updateToken} onRemember={updateRemember} />
        </Show>

        <Switch>
          <Match when={view() === "overview"}>
            <Overview caps={caps()} health={health()} ready={ready()} evidence={evidence()} traces={traces()} />
          </Match>
          <Match when={view() === "capabilities"}>
            <Capabilities caps={caps()} result={capabilities()} />
          </Match>
          <Match when={view() === "evidence"}>
            <Evidence rows={evidenceRows()} result={evidence()} selected={selectedEvidenceID()} onSelect={setSelectedEvidenceID} detail={evidenceDetail()} />
          </Match>
          <Match when={view() === "traces"}>
            <Traces rows={traceRows()} result={traces()} selected={selectedTraceID()} onSelect={setSelectedTraceID} detail={traceDetail()} />
          </Match>
        </Switch>
      </main>
    </div>
  );
}

function AuthPanel(props: {
  token: string;
  remember: boolean;
  onToken: (value: string) => void;
  onRemember: (value: boolean) => void;
}) {
  return (
    <section class="auth-panel">
      <div>
        <h2>Bearer token required</h2>
        <p>JSON operator endpoints share shim ingress auth.</p>
      </div>
      <input
        type="password"
        value={props.token}
        placeholder="Bearer token"
        onInput={(event) => props.onToken(event.currentTarget.value)}
      />
      <label class="check">
        <input type="checkbox" checked={props.remember} onChange={(event) => props.onRemember(event.currentTarget.checked)} />
        Remember in this tab
      </label>
    </section>
  );
}

function Overview(props: {
  caps?: CapabilityManifest;
  health?: APIResult<Record<string, string>>;
  ready?: APIResult<Record<string, string>>;
  evidence?: APIResult<EvidenceList>;
  traces?: APIResult<DebugTraceList>;
}) {
  const runtime = () => props.caps?.runtime;
  const ops = () => runtime()?.ops;
  const routing = () => runtime()?.upstream_provider_routing;
  const persistence = () => runtime()?.persistence;
  const retrieval = () => runtime()?.retrieval;

  return (
    <section class="screen">
      <div class="section-head">
        <h2>Overview</h2>
        <p>Liveness and readiness endpoints are separate from the broader capability probe matrix.</p>
      </div>
      <div class="summary-grid">
        <Metric title="liveness" value={resultLabel(props.health, "ok")} kind={resultKind(props.health)} />
        <Metric title="readiness" value={resultLabel(props.ready, "ready")} kind={resultKind(props.ready)} />
        <Metric title="capability probes" value={capabilitiesLabel(props.caps)} kind={capabilitiesKind(props.caps)} />
        <Metric title="providers" value={String(routing()?.provider_count ?? 0)} kind={routing()?.enabled ? "ready" : "muted"} />
        <Metric title="models" value={String(routing()?.model_count ?? 0)} kind={routing()?.enabled ? "ready" : "muted"} />
        <Metric title="storage" value={asText(persistence()?.backend)} kind="muted" />
        <Metric title="retrieval" value={asText(retrieval()?.index_backend)} kind={retrieval()?.semantic_search ? "ready" : "muted"} />
        <Metric title="debug traces" value={ops()?.debug_traces?.enabled ? `${ops()?.debug_traces?.max_entries ?? 0} entries` : "disabled"} kind={ops()?.debug_traces?.enabled ? "ready" : "muted"} />
        <Metric title="evidence" value={evidenceOverviewValue(props.evidence)} kind={evidenceOverviewKind(props.evidence)} />
        <Metric title="recent traces" value={String(props.traces?.data?.data?.length ?? 0)} kind={props.traces?.ok ? "ready" : "warn"} />
      </div>

      <div class="columns">
        <Panel title="Provider routing">
          <KeyValue label="enabled" value={routing()?.enabled ?? false} />
          <KeyValue label="providers" value={String(routing()?.provider_count ?? 0)} />
          <KeyValue label="models" value={String(routing()?.model_count ?? 0)} />
          <For each={routing()?.providers || []}>
            {(provider) => (
              <div class="provider-line">
                <strong>{provider.id || "-"}</strong>
                <span>{provider.models?.join(", ") || "no models"}</span>
              </div>
            )}
          </For>
        </Panel>

        <Panel title="Operational policy">
          <KeyValue label="auth" value={asText(ops()?.auth_mode)} />
          <KeyValue label="rate limit" value={ops()?.rate_limit?.enabled ? `${ops()?.rate_limit?.requests_per_minute}/min` : "disabled"} />
          <KeyValue label="metrics" value={ops()?.metrics?.enabled ? ops()?.metrics?.path || "/metrics" : "disabled"} />
          <KeyValue label="evidence" value={ops()?.evidence?.enabled ? ops()?.evidence?.root || ".tmp" : "disabled"} />
          <KeyValue label="operator UI" value={ops()?.operator_ui?.enabled ? ops()?.operator_ui?.base_path || "/ui/" : "disabled"} />
        </Panel>
      </div>
    </section>
  );
}

function Capabilities(props: { caps?: CapabilityManifest; result?: APIResult<CapabilityManifest> }) {
  return (
    <section class="screen">
      <div class="section-head">
        <h2>Capabilities</h2>
        <p>Shim-owned runtime manifest. Compatibility labels remain in repo docs.</p>
      </div>
      <Show when={!props.result?.ok && props.result?.error}>
        <ErrorState result={props.result} />
      </Show>
      <Show when={props.caps}>
        <div class="columns">
          <Panel title="Surfaces">
            <ObjectRows value={props.caps?.surfaces} />
          </Panel>
          <Panel title="Runtime">
            <ObjectRows value={props.caps?.runtime} skip={["ops"]} />
          </Panel>
        </div>
        <div class="columns">
          <Panel title="Tools">
            <ToolRows tools={props.caps?.tools || {}} />
          </Panel>
          <Panel title="Probes">
            <For each={Object.entries(props.caps?.probes || {})}>
              {([name, probe]) => (
                <div class="row">
                  <span>{name}</span>
                  <ProbeStatus probe={probe} />
                </div>
              )}
            </For>
          </Panel>
        </div>
        <Panel title="Backends and plugins">
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>id</th>
                  <th>kind</th>
                  <th>class</th>
                  <th>plugin</th>
                  <th>probe</th>
                  <th>ready</th>
                </tr>
              </thead>
              <tbody>
                <For each={props.caps?.backends?.components || []}>
                  {(component) => (
                    <tr>
                      <td>{asText(component.id)}</td>
                      <td>{asText(component.kind)}</td>
                      <td>{asText(component.capability_class)}</td>
                      <td>{asText(component.plugin_id)}</td>
                      <td>{asText(component.readiness_probe)}</td>
                      <td><BackendStatus component={component} /></td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </div>
        </Panel>
      </Show>
    </section>
  );
}

function Evidence(props: {
  rows: EvidenceRecord[];
  result?: APIResult<EvidenceList>;
  selected: string;
  onSelect: (id: string) => void;
  detail?: APIResult<EvidenceDetail | undefined>;
}) {
  const latest = () => props.result?.data?.latest_by_kind || [];
  const opsReport = () => latest().find((record) => record.kind === "v4_provider_ops");
  const latestCards = () => {
    const ops = opsReport();
    const rest = latest().filter((record) => record.id !== ops?.id);
    return ops ? [ops, ...rest].slice(0, 8) : rest.slice(0, 8);
  };
  return (
    <section class="screen">
      <div class="section-head">
        <h2>Evidence</h2>
        <p>Read-only operational summaries from known local artifact directories. Raw logs, prompts, headers, and file contents are not read.</p>
      </div>
      <Show when={!props.result?.ok && props.result?.status !== 404 && props.result?.error}>
        <ErrorState result={props.result} />
      </Show>
      <Show when={props.result?.status === 404}>
        <div class="empty">Operational evidence is disabled.</div>
      </Show>
      <Show when={props.result?.data?.errors && props.result.data.errors.length > 0}>
        <Panel title="Scan warnings">
          <For each={props.result?.data?.errors || []}>
            {(error) => <KeyValue label={error.kind || error.path || "scan"} value={error.message || "scan error"} />}
          </For>
        </Panel>
      </Show>
      <Show when={opsReport()}>
        {(record) => (
          <Panel title="Provider Ops">
            <KeyValue label="status" value={record().status || "-"} />
            <KeyValue label="verdict" value={record().verdict || "-"} />
            <KeyValue label="age" value={formatAge(record().age_seconds)} />
            <KeyValue label="artifact" value={record().artifact_dir || "-"} />
          </Panel>
        )}
      </Show>
      <Show when={latest().length > 0}>
        <div class="summary-grid">
          <For each={latestCards()}>
            {(record) => (
              <Metric
                title={shortEvidenceKind(record.kind)}
                value={record.status || "-"}
                kind={evidenceStatusKind(record)}
              />
            )}
          </For>
        </div>
      </Show>
      <div class="trace-layout">
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>kind</th>
                <th>status</th>
                <th>model</th>
                <th>age</th>
                <th>artifact</th>
              </tr>
            </thead>
            <tbody>
              <For each={props.rows}>
                {(record) => (
                  <tr classList={{ selected: props.selected === record.id }} onClick={() => props.onSelect(record.id)}>
                    <td>{shortEvidenceKind(record.kind)}</td>
                    <td><StatusPill label="" kind={evidenceStatusKind(record)} value={record.status || "-"} /></td>
                    <td>{record.model || "-"}</td>
                    <td>{formatAge(record.age_seconds)}</td>
                    <td class="mono">{record.artifact_dir || "-"}</td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
          <Show when={props.rows.length === 0 && props.result?.ok}>
            <div class="empty">No evidence artifacts found under {props.result?.data?.root || ".tmp"}.</div>
          </Show>
        </div>
        <aside class="inspector">
          <Show when={props.selected !== ""} fallback={<div class="empty">Select an evidence row.</div>}>
            <Show when={props.detail?.data} fallback={<ErrorState result={props.detail} />}>
              <EvidenceDetailView detail={props.detail?.data} />
            </Show>
          </Show>
        </aside>
      </div>
    </section>
  );
}

function EvidenceDetailView(props: { detail?: EvidenceDetail }) {
  const record = () => props.detail?.evidence;
  return (
    <div>
      <h3>{record()?.title || record()?.id}</h3>
      <KeyValue label="kind" value={record()?.kind || "-"} />
      <KeyValue label="status" value={record()?.status || "-"} />
      <KeyValue label="verdict" value={record()?.verdict || "-"} />
      <KeyValue label="model" value={record()?.model || "-"} />
      <KeyValue label="stale" value={record()?.stale ?? false} />
      <KeyValue label="generated" value={record()?.generated_at || "-"} />
      <KeyValue label="summary" value={record()?.summary_path || "-"} />
      <KeyValue label="summary md" value={record()?.summary_md_path || "-"} />
      <KeyValue label="redaction" value={props.detail?.redaction_policy || "-"} />
      <DetailBlock title="metrics" value={record()?.metrics} />
      <DetailBlock title="summary json" value={props.detail?.summary} />
    </div>
  );
}

function Traces(props: {
  rows: DebugTrace[];
  result?: APIResult<DebugTraceList>;
  selected: string;
  onSelect: (id: string) => void;
  detail?: APIResult<DebugTrace | undefined>;
}) {
  return (
    <section class="screen">
      <div class="section-head">
        <h2>Traces</h2>
        <p>Recent metadata-only requests. Prompts, outputs, headers, files, and tokens are not displayed.</p>
      </div>
      <Show when={!props.result?.ok && props.result?.status !== 404 && props.result?.error}>
        <ErrorState result={props.result} />
      </Show>
      <Show when={props.result?.status === 404}>
        <div class="empty">Debug traces are disabled.</div>
      </Show>
      <div class="trace-layout">
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>request</th>
                <th>route</th>
                <th>model</th>
                <th>backend</th>
                <th>status</th>
                <th>ms</th>
              </tr>
            </thead>
            <tbody>
              <For each={props.rows}>
                {(trace) => (
                  <tr classList={{ selected: props.selected === trace.request_id }} onClick={() => props.onSelect(trace.request_id)}>
                    <td class="mono">{trace.request_id}</td>
                    <td>{trace.route || trace.path || "-"}</td>
                    <td>{trace.public_model || trace.model || "-"}</td>
                    <td>{trace.selected_backend || "-"}</td>
                    <td><StatusPill label="" kind={statusFromHTTP(trace.final_status)} value={asText(trace.final_status)} /></td>
                    <td>{asText(trace.duration_ms)}</td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
          <Show when={props.rows.length === 0 && props.result?.ok}>
            <div class="empty">No trace entries yet.</div>
          </Show>
        </div>
        <aside class="inspector">
          <Show when={props.selected !== ""} fallback={<div class="empty">Select a trace.</div>}>
            <Show when={props.detail?.data} fallback={<ErrorState result={props.detail} />}>
              <TraceDetail trace={props.detail?.data} />
            </Show>
          </Show>
        </aside>
      </div>
    </section>
  );
}

function TraceDetail(props: { trace?: DebugTrace }) {
  const trace = () => props.trace;
  return (
    <div>
      <h3 class="mono">{trace()?.request_id}</h3>
      <KeyValue label="client request" value={trace()?.client_request_id || "-"} />
      <KeyValue label="route" value={trace()?.route || trace()?.path || "-"} />
      <KeyValue label="surface" value={trace()?.surface || "-"} />
      <KeyValue label="provider" value={trace()?.provider || "-"} />
      <KeyValue label="public model" value={trace()?.public_model || trace()?.model || "-"} />
      <KeyValue label="upstream model" value={trace()?.upstream_model || "-"} />
      <KeyValue label="plugin" value={trace()?.plugin_id || "-"} />
      <KeyValue label="routing" value={trace()?.routing_mode || "-"} />
      <KeyValue label="projection" value={trace()?.backend_projection_class || "-"} />
      <KeyValue label="replay" value={trace()?.replay_class || "-"} />
      <KeyValue label="stream" value={trace()?.stream_transformer_class || "-"} />
      <KeyValue label="redaction" value={trace()?.redaction_policy || "-"} />
      <DetailBlock title="tool decisions" value={trace()?.tool_decisions} />
      <DetailBlock title="transforms" value={trace()?.transforms} />
      <DetailBlock title="backend failure" value={trace()?.backend_failure} />
      <DetailBlock title="fallback" value={trace()?.fallback} />
      <DetailBlock title="rate limit" value={trace()?.rate_limit} />
    </div>
  );
}

function Metric(props: { title: string; value: string; kind: StatusKind }) {
  return (
    <div class={`metric ${props.kind}`}>
      <span>{props.title}</span>
      <strong>{props.value}</strong>
    </div>
  );
}

function Panel(props: { title: string; children: unknown }) {
  return (
    <section class="panel">
      <h3>{props.title}</h3>
      {props.children}
    </section>
  );
}

function KeyValue(props: { label: string; value: unknown }) {
  return (
    <div class="row">
      <span>{props.label}</span>
      <strong><Value value={props.value} /></strong>
    </div>
  );
}

function StatusPill(props: { label: string; value: string; kind: StatusKind }) {
  return (
    <span class={`status ${props.kind}`}>
      <Show when={props.label !== ""}><span>{props.label}</span></Show>
      <strong>{props.value}</strong>
    </span>
  );
}

function ProbeStatus(props: { probe: { enabled?: boolean; checked?: boolean; ready?: boolean; error?: string } }) {
  const state = () => {
    if (props.probe.enabled === false) {
      return { kind: "muted" as StatusKind, value: "disabled" };
    }
    if (props.probe.ready) {
      return { kind: "ready" as StatusKind, value: "ready" };
    }
    if (!props.probe.checked) {
      return { kind: "muted" as StatusKind, value: "not checked" };
    }
    return { kind: "error" as StatusKind, value: props.probe.error || "not ready" };
  };
  return <StatusPill label="" kind={state().kind} value={state().value} />;
}

function BackendStatus(props: { component: Record<string, unknown> }) {
  const state = () => {
    if (props.component.enabled === false) {
      return { kind: "muted" as StatusKind, value: "disabled" };
    }
    if (props.component.ready === true) {
      return { kind: "ready" as StatusKind, value: "ready" };
    }
    const probe = asText(props.component.readiness_probe, "");
    return { kind: "error" as StatusKind, value: probe ? `not ready: ${probe}` : "not ready" };
  };
  return <StatusPill label="" kind={state().kind} value={state().value} />;
}

function ObjectRows(props: { value?: Record<string, unknown>; skip?: string[] }) {
  const skip = () => new Set(props.skip || []);
  return (
    <For each={Object.entries(props.value || {}).filter(([key]) => !skip().has(key))}>
      {([key, value]) => <KeyValue label={key} value={summarizeValue(key, value)} />}
    </For>
  );
}

function ToolRows(props: { tools: Record<string, Record<string, unknown>> }) {
  return (
    <For each={Object.entries(props.tools)}>
      {([name, tool]) => (
        <div class="row">
          <span>{name}</span>
          <strong>{asText(tool.disposition || tool.support)}</strong>
        </div>
      )}
    </For>
  );
}

function DetailBlock(props: { title: string; value: unknown }) {
  return (
    <Show when={props.value !== undefined && props.value !== null}>
      <details>
        <summary>{props.title}</summary>
        <pre>{compactJSON(props.value)}</pre>
      </details>
    </Show>
  );
}

function ErrorState(props: { result?: APIResult<unknown> }) {
  const result = () => props.result;
  return <div class="empty">{result()?.error || (result()?.status ? `HTTP ${result()?.status}` : "Loading")}</div>;
}

function Value(props: { value: unknown }) {
  return (
    <Show when={typeof props.value === "boolean"} fallback={<>{formatShort(props.value)}</>}>
      <BoolIcon value={Boolean(props.value)} />
    </Show>
  );
}

function BoolIcon(props: { value: boolean }) {
  const label = () => (props.value ? "true" : "false");
  return <span class={`bool-icon ${props.value ? "yes" : "no"}`} title={label()} aria-label={label()} />;
}

function resultKind(result?: APIResult<unknown>): StatusKind {
  if (!result) {
    return "muted";
  }
  if (result.ok) {
    return "ready";
  }
  if (result.status === 401 || result.status === 403) {
    return "warn";
  }
  return "error";
}

function resultLabel(result: APIResult<Record<string, string>> | undefined, field: string): string {
  if (!result) {
    return "loading";
  }
  if (result.ok) {
    return result.data?.[field] || "ok";
  }
  return result.status === 0 ? "offline" : `HTTP ${result.status}`;
}

function statusFromHTTP(status?: number): StatusKind {
  const value = asNumber(status);
  if (value === undefined) {
    return "muted";
  }
  if (value >= 200 && value < 300) {
    return "ready";
  }
  if (value >= 400 && value < 500) {
    return "warn";
  }
  return "error";
}

function capabilitiesKind(caps?: CapabilityManifest): StatusKind {
  if (!caps) {
    return "muted";
  }
  return caps.ready ? "ready" : "warn";
}

function capabilitiesLabel(caps?: CapabilityManifest): string {
  if (!caps) {
    return "loading";
  }
  return caps.ready ? "ready" : "degraded";
}

function evidenceOverviewValue(result?: APIResult<EvidenceList>): string {
  if (!result) {
    return "loading";
  }
  if (!result.ok) {
    return result.status === 404 ? "disabled" : `HTTP ${result.status}`;
  }
  const rows = result.data?.data || [];
  const stale = rows.filter((row) => row.stale).length;
  return stale > 0 ? `${rows.length} runs, ${stale} stale` : `${rows.length} runs`;
}

function evidenceOverviewKind(result?: APIResult<EvidenceList>): StatusKind {
  if (!result) {
    return "muted";
  }
  if (!result.ok) {
    return result.status === 404 ? "muted" : "warn";
  }
  const rows = result.data?.data || [];
  if (rows.some((row) => evidenceStatusKind(row) === "error")) {
    return "error";
  }
  if (rows.some((row) => row.stale || evidenceStatusKind(row) === "warn")) {
    return "warn";
  }
  return rows.length > 0 ? "ready" : "muted";
}

function evidenceStatusKind(record: EvidenceRecord): StatusKind {
  if (record.error) {
    return "error";
  }
  const value = (record.status || record.verdict || "").toLowerCase();
  if (value === "passed" || value === "release_gate_ok" || value === "ok") {
    return record.stale ? "warn" : "ready";
  }
  if (value === "skipped" || value === "disabled" || value === "unknown") {
    return "muted";
  }
  if (value === "failed" || value === "error" || value === "unreadable") {
    return "error";
  }
  return record.stale ? "warn" : "muted";
}

function shortEvidenceKind(kind: string): string {
  return kind
    .replace(/^v[34]_/, "")
    .replace(/_/g, " ")
    .replace("provider matrix", "matrix")
    .replace("upstream provider routing", "routing");
}

function formatAge(seconds?: number): string {
  const value = asNumber(seconds);
  if (value === undefined) {
    return "-";
  }
  if (value < 60) {
    return `${Math.max(0, Math.floor(value))}s`;
  }
  if (value < 3600) {
    return `${Math.floor(value / 60)}m`;
  }
  if (value < 86400) {
    return `${Math.floor(value / 3600)}h`;
  }
  return `${Math.floor(value / 86400)}d`;
}

function summarizeValue(label: string, value: unknown): unknown {
  if (value === undefined || value === null || typeof value !== "object" || Array.isArray(value)) {
    return value;
  }

  const record = value as Record<string, unknown>;
  const parts: string[] = [];

  if (typeof record.enabled === "boolean") {
    parts.push(record.enabled ? "enabled" : "disabled");
  }
  if (typeof record.ready === "boolean") {
    parts.push(record.ready ? "ready" : "not ready");
  }
  if (typeof record.checked === "boolean" && typeof record.ready !== "boolean") {
    parts.push(record.checked ? "checked" : "not checked");
  }

  const priorityFields = prioritySummaryFields(label);
  for (const key of priorityFields) {
    const item = record[key];
    if (item === undefined || item === null) {
      continue;
    }
    if (typeof item === "boolean") {
      parts.push(`${key} ${item ? "on" : "off"}`);
      continue;
    }
    if (Array.isArray(item)) {
      parts.push(`${key} ${item.length}`);
      continue;
    }
    if (typeof item !== "object") {
      parts.push(`${key} ${asText(item)}`);
    }
  }

  if (typeof record.provider_count === "number") {
    parts.push(`${record.provider_count} providers`);
  }
  if (typeof record.model_count === "number") {
    parts.push(`${record.model_count} models`);
  }
  if (Array.isArray(record.providers)) {
    parts.push(`${record.providers.length} provider rows`);
  }
  if (Array.isArray(record.models)) {
    parts.push(`${record.models.length} models`);
  }
  if (Array.isArray(record.captures)) {
    parts.push(`${record.captures.length} captures`);
  }

  if (parts.length > 0) {
    return parts.slice(0, 4).join(" · ");
  }

  const keys = Object.keys(record);
  if (keys.length === 0) {
    return "empty";
  }
  return `${keys.length} settings: ${keys.slice(0, 4).join(", ")}${keys.length > 4 ? ", ..." : ""}`;
}

function prioritySummaryFields(label: string): string[] {
  const common = [
    "support",
    "disposition",
    "mode",
    "transport",
    "backend",
    "storage_backend",
    "object_storage_backend",
    "index_backend",
    "embedder_backend",
    "runtime",
    "auth_mode",
    "base_path",
    "path",
    "redaction",
  ];

  switch (label) {
    case "responses":
      return ["mode", "transport", "create_stream", "retrieve_stream", "websocket", ...common];
    case "chat_completions":
      return ["mode", "transport", "stream", "store", ...common];
    case "upstream_provider_routing":
      return ["selection", "routing_mode", ...common];
    case "persistence":
      return ["backend", "storage_backend", "object_storage_backend", "hard_delete", ...common];
    case "retrieval":
      return ["index_backend", "embedder_backend", "semantic_search", "ann_index", ...common];
    default:
      return common;
  }
}

function formatShort(value: unknown): string {
  if (Array.isArray(value)) {
    return `${value.length} items`;
  }
  if (value && typeof value === "object") {
    const keys = Object.keys(value as Record<string, unknown>);
    return `${keys.length} settings${keys.length > 0 ? `: ${keys.slice(0, 4).join(", ")}${keys.length > 4 ? ", ..." : ""}` : ""}`;
  }
  return asText(value);
}
