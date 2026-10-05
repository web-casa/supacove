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

type Overview = operations["getOverview"]["responses"]["200"]["content"]["application/json"];
type OverviewEntry = Overview["databases"][number];
type TaskList = operations["listTasks"]["responses"]["200"]["content"]["application/json"];
type Task = TaskList["tasks"][number];
type DatabaseList = operations["listDatabases"]["responses"]["200"]["content"]["application/json"];
type Database = DatabaseList["databases"][number];
type DatabaseCreate = operations["createDatabase"]["requestBody"]["content"]["application/json"];
type ScheduleConfig = operations["getDatabaseSchedule"]["responses"]["200"]["content"]["application/json"];
type ScheduleUpdate = operations["putDatabaseSchedule"]["requestBody"]["content"]["application/json"];
type WebhookList = operations["listWebhooks"]["responses"]["200"]["content"]["application/json"];
type Webhook = WebhookList["webhooks"][number];
type WebhookCreate = operations["createWebhook"]["requestBody"]["content"]["application/json"];
type WebhookTest = operations["testWebhook"]["responses"]["200"]["content"]["application/json"];
type NotificationList = operations["listNotifications"]["responses"]["200"]["content"]["application/json"];
type StatsSummary = operations["getStats"]["responses"]["200"]["content"]["application/json"];

export type {
  Overview,
  OverviewEntry,
  Task,
  StatsSummary,
  WebhookList,
  Database,
  DatabaseCreate,
  ScheduleConfig,
  ScheduleUpdate,
  Webhook,
  WebhookCreate,
  WebhookTest,
  NotificationList,
};

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

  // Phase 7 surface.
  overview: () => request<Overview>("/api/overview"),
  databases: () => request<DatabaseList>("/api/databases"),
  createDatabase: (body: DatabaseCreate) =>
    request<Database>("/api/databases", { method: "POST", body: JSON.stringify(body) }),
  deleteDatabase: (id: number) => request<void>(`/api/databases/${id}`, { method: "DELETE" }),
  backupNow: (id: number) =>
    request<operations["triggerBackup"]["responses"]["202"]["content"]["application/json"]>(
      `/api/databases/${id}/backups`,
      { method: "POST" },
    ),
  tasks: () => request<TaskList>("/api/tasks"),
  getSchedule: (id: number) => request<ScheduleConfig>(`/api/databases/${id}/schedule`),
  putSchedule: (id: number, body: ScheduleUpdate) =>
    request<ScheduleConfig>(`/api/databases/${id}/schedule`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  webhooks: () => request<WebhookList>("/api/webhooks"),
  createWebhook: (body: WebhookCreate) =>
    request<Webhook>("/api/webhooks", { method: "POST", body: JSON.stringify(body) }),
  deleteWebhook: (id: number) => request<void>(`/api/webhooks/${id}`, { method: "DELETE" }),
  testWebhook: (body: WebhookCreate) =>
    request<WebhookTest>("/api/webhooks/test", { method: "POST", body: JSON.stringify(body) }),
  notifications: () => request<NotificationList>("/api/notifications"),
  stats: () => request<StatsSummary>("/api/stats"),
};

// Kit and artifact downloads are same-origin GETs carrying the session
// cookie; plain navigation links are the simplest correct delivery.
export const kitDownloadPath = (taskId: number) => `/api/tasks/${taskId}/recovery-kit`;
export const artifactDownloadPath = (taskId: number) => `/api/tasks/${taskId}/download`;
export const presignedUrl = async (taskId: number): Promise<string> => {
  const r = await request<{ url: string }>(`/api/tasks/${taskId}/download-url`);
  return r.url;
};
