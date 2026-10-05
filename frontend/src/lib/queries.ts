// Shared query definitions so every panel reading the same resource agrees
// on its key and polling cadence.
import { queryOptions } from "@tanstack/react-query";
import { api } from "../api/client";

export const overviewQuery = queryOptions({
  queryKey: ["overview"],
  queryFn: api.overview,
  refetchInterval: 30_000,
});

export const statsQuery = queryOptions({
  queryKey: ["stats"],
  queryFn: api.stats,
  refetchInterval: 60_000,
});

export const tasksQuery = queryOptions({
  queryKey: ["tasks"],
  queryFn: api.tasks,
  refetchInterval: 15_000,
});

export const webhooksQuery = queryOptions({
  queryKey: ["webhooks"],
  queryFn: api.webhooks,
});

export const notificationsQuery = queryOptions({
  queryKey: ["notifications"],
  queryFn: api.notifications,
  refetchInterval: 20_000,
});
