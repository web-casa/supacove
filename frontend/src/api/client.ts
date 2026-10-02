// Typed API client generated from the OpenAPI contract (types) plus a thin
// runtime wrapper. Generated types do not replace server-side validation.
import type { operations } from "./schema.d";

type User = operations["getAuthMe"]["responses"]["200"]["content"]["application/json"];
type HealthDetails = operations["getHealthDetails"]["responses"]["200"]["content"]["application/json"];
type HealthStatus = operations["getReady"]["responses"]["200"]["content"]["application/json"];

export type { User, HealthDetails, HealthStatus };

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

function csrfToken(): string {
  const match = document.cookie.match(/(?:^|;\s*)sb_csrf=([^;]*)/);
  return match ? decodeURIComponent(match[1]) : "";
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body) headers.set("Content-Type", "application/json");
  if (init?.method && init.method !== "GET") headers.set("X-CSRF-Token", csrfToken());

  const resp = await fetch(path, { ...init, headers, credentials: "same-origin" });
  if (resp.status === 204) return undefined as T;

  const body = (await resp.json().catch(() => ({}))) as {
    code?: string;
    message?: string;
  };
  if (!resp.ok) {
    throw new ApiError(resp.status, body.code ?? "unknown", body.message ?? resp.statusText);
  }
  return body as T;
}

export const api = {
  me: () => request<User>("/api/auth/me"),
  login: (username: string, password: string) =>
    request<User>("/api/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
  bootstrap: (token: string, username: string, password: string) =>
    request<User>("/api/auth/bootstrap", {
      method: "POST",
      body: JSON.stringify({ token, username, password }),
    }),
  logout: () => request<void>("/api/auth/logout", { method: "POST" }),
  healthDetails: () => request<HealthDetails>("/api/health/details"),
  ready: () => request<HealthStatus>("/api/ready"),
};
