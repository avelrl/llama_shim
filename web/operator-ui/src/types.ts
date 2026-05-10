export type APIErrorPayload = {
  error?: {
    message?: string;
    type?: string;
    code?: string | null;
    param?: string | null;
  };
};

export type APIResult<T> = {
  ok: boolean;
  status: number;
  data?: T;
  error?: string;
};

export type CapabilityManifest = {
  object: "shim.capabilities";
  ready: boolean;
  surfaces?: Record<string, unknown>;
  runtime?: {
    responses_mode?: string;
    responses_upstream_transport?: string;
    custom_tools_mode?: string;
    upstream_provider_routing?: {
      enabled?: boolean;
      provider_count?: number;
      model_count?: number;
      providers?: Array<{ id?: string; plugin_id?: string; model_count?: number; models?: string[] }>;
    };
    persistence?: Record<string, unknown>;
    retrieval?: Record<string, unknown>;
    memory?: Record<string, unknown>;
    compaction?: Record<string, unknown>;
    ops?: {
      auth_mode?: string;
      rate_limit?: { enabled?: boolean; requests_per_minute?: number; burst?: number };
      metrics?: { enabled?: boolean; path?: string };
      debug_traces?: {
        enabled?: boolean;
        max_entries?: number;
        list_endpoint?: string;
        detail_endpoint?: string;
        redaction?: string;
        captures?: string[];
      };
      evidence?: {
        enabled?: boolean;
        root?: string;
        max_entries?: number;
        stale_after_seconds?: number;
        list_endpoint?: string;
        detail_endpoint?: string;
        redaction?: string;
        support?: string;
      };
      operator_ui?: {
        enabled?: boolean;
        base_path?: string;
        public_static_assets?: boolean;
        support?: string;
      };
    };
  };
  tools?: Record<string, Record<string, unknown>>;
  backends?: { schema_version?: string; components?: Array<Record<string, unknown>> };
  plugins?: { schema_version?: string; plugins?: Array<Record<string, unknown>> };
  probes?: Record<string, { enabled?: boolean; checked?: boolean; ready?: boolean; error?: string }>;
};

export type DebugTrace = {
  object: "shim.debug_trace";
  request_id: string;
  client_request_id?: string;
  method?: string;
  path?: string;
  route?: string;
  surface?: string;
  source_format?: string;
  model?: string;
  provider?: string;
  public_model?: string;
  upstream_model?: string;
  plugin_id?: string;
  plugin_version?: string;
  plugin_contract_version?: string;
  routing_mode?: string;
  upstream_transport?: string;
  selected_backend?: string;
  backend_projection_class?: string;
  persistence_decision?: string;
  replay_class?: string;
  stream_transformer_class?: string;
  tool_decisions?: Array<Record<string, unknown>>;
  transforms?: Array<Record<string, unknown>>;
  backend_failure?: Record<string, unknown>;
  fallback?: Record<string, unknown>;
  rate_limit?: Record<string, unknown>;
  final_status?: number;
  started_at?: string;
  completed_at?: string;
  duration_ms?: number;
  response_content_type?: string;
  redaction_policy?: string;
};

export type DebugTraceList = {
  object: "list";
  data: DebugTrace[];
};

export type EvidenceRecord = {
  id: string;
  kind: string;
  title?: string;
  status?: string;
  verdict?: string;
  model?: string;
  artifact_dir?: string;
  summary_path?: string;
  summary_md_path?: string;
  generated_at?: string;
  modified_at?: string;
  age_seconds?: number;
  stale?: boolean;
  warning_count?: number;
  failure_count?: number;
  metrics?: Record<string, unknown>;
  error?: string;
};

export type EvidenceList = {
  object: "shim.evidence_list";
  generated_at?: string;
  root?: string;
  max_entries?: number;
  stale_after_seconds?: number;
  redaction_policy?: string;
  sources?: Array<{ kind?: string; title?: string; glob?: string }>;
  latest_by_kind?: EvidenceRecord[];
  data: EvidenceRecord[];
  errors?: Array<{ kind?: string; path?: string; message?: string }>;
};

export type EvidenceDetail = {
  object: "shim.evidence";
  redaction_policy?: string;
  evidence: EvidenceRecord;
  summary?: Record<string, unknown>;
};
