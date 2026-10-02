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
  // Production uses the __Host- prefix; plain-HTTP dev uses the bare name.
  for (const name of ["__Host-sb_csrf", "sb_csrf"]) {
    const match = document.cookie.match(
      new RegExp(`(?:^|;\\s*)${name}=([^;]*)`),
    );
    if (match) return decodeURIComponent(match[1]);
  }
  return "";
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
  // me() resolves to null on 401 so stale cached users never keep the UI in
  // an authenticated state after logout or session expiry (review P1-11).
  me: async (): Promise<User | null> => {
    try {
      return await request<User>("/api/auth/me");
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) return null;
      throw e;
    }
  },
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
