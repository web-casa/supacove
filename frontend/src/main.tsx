import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import App from "./App";
import { ApiError } from "./api/client";
import "@fontsource-variable/geist";
import "@fontsource-variable/geist-mono";
import "./index.css";

// Any 401 outside the auth probe means the session is gone (expired or
// revoked): flip ["me"] to null so App renders the login screen, and drop
// every authenticated cache entry so stale data cannot reappear.
function onAuthLost(error: unknown) {
  if (!(error instanceof ApiError) || error.status !== 401) return;
  if (queryClient.getQueryData(["me"]) == null) return; // already signed out (e.g. a bad login)
  queryClient.setQueryData(["me"], null);
  queryClient.removeQueries({
    predicate: (q) => q.queryKey[0] !== "me" && q.queryKey[0] !== "ready",
  });
}

const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError: onAuthLost }),
  mutationCache: new MutationCache({ onError: onAuthLost }),
  defaultOptions: {
    queries: {
      retry: (failures, error) => !(error instanceof ApiError && error.status === 401) && failures < 1,
      refetchOnWindowFocus: false,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
