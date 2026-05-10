import type { APIErrorPayload, APIResult } from "./types";

export async function fetchJSON<T>(path: string, token: string): Promise<APIResult<T>> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const cleanToken = token.trim();
  if (cleanToken !== "") {
    headers.Authorization = `Bearer ${cleanToken}`;
  }

  let response: Response;
  try {
    response = await fetch(path, { headers });
  } catch (error) {
    return {
      ok: false,
      status: 0,
      error: error instanceof Error ? error.message : "request failed"
    };
  }

  const text = await response.text();
  let parsed: unknown = undefined;
  if (text.trim() !== "") {
    try {
      parsed = JSON.parse(text);
    } catch {
      return {
        ok: false,
        status: response.status,
        error: `non-JSON response (${response.status})`
      };
    }
  }

  if (!response.ok) {
    const payload = parsed as APIErrorPayload;
    return {
      ok: false,
      status: response.status,
      error: payload.error?.message || response.statusText || `HTTP ${response.status}`
    };
  }

  return { ok: true, status: response.status, data: parsed as T };
}

export function asText(value: unknown, fallback = "-"): string {
  if (value === null || value === undefined || value === "") {
    return fallback;
  }
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  return fallback;
}

export function asNumber(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

export function compactJSON(value: unknown): string {
  if (value === null || value === undefined) {
    return "-";
  }
  return JSON.stringify(value, null, 2);
}
