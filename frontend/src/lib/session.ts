// Session-aware QueryClient: any 401 outside the auth probe means the
// session is gone (expired or revoked) — flip ["me"] to null so App renders
// the login screen, and drop every authenticated cache entry so a late or
// stale response can never resurrect old session data (review P1-11).
import { MutationCache, QueryCache, QueryClient } from "@tanstack/react-query";
import { ApiError } from "../api/client";

export function newSessionQueryClient(): QueryClient {
  const qc: QueryClient = new QueryClient({
    queryCache: new QueryCache({ onError: (e: unknown) => onAuthLost(qc, e) }),
    mutationCache: new MutationCache({
      onError: (e: unknown) => onAuthLost(qc, e),
    }),
    defaultOptions: {
      queries: {
        retry: (failures, error) =>
          !(error instanceof ApiError && error.status === 401) && failures < 1,
        refetchOnWindowFocus: false,
      },
    },
  });
  return qc;
}

function onAuthLost(qc: QueryClient, error: unknown): void {
  if (!(error instanceof ApiError) || error.status !== 401) return;
  if (qc.getQueryData(["me"]) == null) return; // already signed out (e.g. a bad login)
  qc.setQueryData(["me"], null);
  qc.removeQueries({
    predicate: (q) => q.queryKey[0] !== "me" && q.queryKey[0] !== "ready",
  });
}
