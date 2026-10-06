import { describe, expect, it } from "vitest";
import { ApiError } from "../api/client";
import { newSessionQueryClient } from "./session";

async function failWith(
  qc: ReturnType<typeof newSessionQueryClient>,
  status: number,
) {
  await qc
    .fetchQuery({
      queryKey: ["probe", status],
      queryFn: () => Promise.reject(new ApiError(status, "code", "msg")),
      retry: false,
    })
    .catch(() => undefined);
}

describe("session loss (401)", () => {
  it("flips me to null and drops authenticated caches", async () => {
    const qc = newSessionQueryClient();
    qc.setQueryData(["me"], { id: 1, username: "admin", createdAt: 0 });
    qc.setQueryData(["overview"], { databases: [{ databaseId: 1 }] });
    qc.setQueryData(["tasks"], { tasks: [{ id: 1 }] });
    qc.setQueryData(["ready"], { status: "ok" });

    await failWith(qc, 401);

    expect(qc.getQueryData(["me"])).toBeNull();
    expect(qc.getQueryData(["overview"])).toBeUndefined();
    expect(qc.getQueryData(["tasks"])).toBeUndefined();
    // the auth probe and readiness stay (they are the recovery path)
    expect(qc.getQueryData(["ready"])).toEqual({ status: "ok" });
  });

  it("ignores non-401 errors", async () => {
    const qc = newSessionQueryClient();
    qc.setQueryData(["me"], { id: 1, username: "admin", createdAt: 0 });
    qc.setQueryData(["overview"], { databases: [] });

    await failWith(qc, 500);

    expect(qc.getQueryData(["me"])).toEqual({
      id: 1,
      username: "admin",
      createdAt: 0,
    });
    expect(qc.getQueryData(["overview"])).toBeDefined();
  });

  it("does not double-clear when already signed out", async () => {
    const qc = newSessionQueryClient();
    qc.setQueryData(["overview"], { databases: [] });

    await failWith(qc, 401);

    // no ["me"] entry means already signed out: caches stay untouched
    expect(qc.getQueryData(["overview"])).toBeDefined();
  });
});

describe("default retry predicate (401 must never retry)", () => {
  // The predicate lives on the client's defaultOptions; exercise it through
  // the public surface: a query WITHOUT an explicit retry option must fail
  // fast on 401 (the whole point of the guard) while a 500 retries once.
  it("401 fails without retries", async () => {
    const qc = newSessionQueryClient();
    let attempts = 0;
    await qc
      .fetchQuery({
        queryKey: ["p401"],
        queryFn: () => {
          attempts++;
          return Promise.reject(new ApiError(401, "unauthenticated", "login required"));
        },
        // retry deliberately NOT overridden: defaults must apply
        staleTime: 0,
        gcTime: 0,
      })
      .catch(() => undefined);
    expect(attempts).toBe(1);
  });

  it("500 retries exactly once (failures<1) then surfaces", async () => {
    const qc = newSessionQueryClient();
    let attempts = 0;
    await qc
      .fetchQuery({
        queryKey: ["p500"],
        queryFn: () => {
          attempts++;
          return Promise.reject(new ApiError(500, "internal", "boom"));
        },
      })
      .catch(() => undefined);
    expect(attempts).toBe(2); // initial + exactly one retry
  });

  it("predicate itself: 401→false, 500 first failure→true, second→false", () => {
    const qc = newSessionQueryClient();
    // The retry function is reachable via defaultOptions after construction.
    const retry = qc.getDefaultOptions().queries?.retry;
    expect(typeof retry).toBe("function");
    const fn = retry as (f: number, e: unknown) => boolean;
    expect(fn(0, new ApiError(401, "unauthenticated", "x"))).toBe(false);
    expect(fn(0, new ApiError(500, "internal", "x"))).toBe(true);
    expect(fn(1, new ApiError(500, "internal", "x"))).toBe(false);
  });
});
