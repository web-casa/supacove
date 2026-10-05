import { useQuery } from "@tanstack/react-query";
import { ServerCrash } from "lucide-react";
import { api, type User } from "./api/client";
import { useI18n } from "./i18n";
import { AuthScreen } from "./components/AuthScreen";
import { Dashboard } from "./components/Dashboard";
import { SplashLayout } from "./components/SplashLayout";
import { InlineMessage } from "./components/ui/InlineMessage";
import { Spinner } from "./components/ui/Spinner";
import { ToastProvider } from "./components/ui/Toaster";

// Flow: me() resolves to an explicit `null` on 401 (never a stale cached
// user), so rendering is driven by real authentication state. A fresh
// instance offers the bootstrap form via an explicit UI entry on the login
// screen (review P1-10) — readiness is NOT used to infer initialization.
// A 401 on any other request flips ["me"] to null in main.tsx, which lands
// here and renders the login screen.

export default function App() {
  const { t } = useI18n();
  const me = useQuery<User | null, Error>({
    queryKey: ["me"],
    queryFn: api.me,
    retry: false,
    staleTime: 30_000,
  });
  const ready = useQuery({
    queryKey: ["ready"],
    queryFn: api.ready,
    retry: false,
    refetchInterval: (q) => (q.state.status === "error" ? 5_000 : false),
  });

  if (me.isPending || ready.isPending) {
    return (
      <SplashLayout>
        <p className="muted splash-status">
          <Spinner /> {t("app.checking")}
        </p>
      </SplashLayout>
    );
  }
  if (ready.isError) {
    return (
      <SplashLayout>
        <div className="auth-card">
          <ServerCrash size={22} className="muted" aria-hidden />
          <h1>{t("app.serviceUnavailable")}</h1>
          <InlineMessage>{t("app.backendDown")}</InlineMessage>
        </div>
      </SplashLayout>
    );
  }
  return (
    <ToastProvider>{me.data ? <Dashboard user={me.data} /> : <AuthScreen />}</ToastProvider>
  );
}
