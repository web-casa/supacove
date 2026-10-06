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
