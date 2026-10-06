import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useLayoutEffect, useRef } from "react";
import {
  Bell,
  History,
  LayoutDashboard,
  LogOut,
  type LucideIcon,
} from "lucide-react";
import { api, type User } from "../api/client";
import { useI18n } from "../i18n";
import { useHashView, type View } from "../lib/useHashView";
import { Brand } from "./Brand";
import { DeliveryLog } from "./DeliveryLog";
import { HealthSummary } from "./HealthSummary";
import { PipelinePanel } from "./PipelinePanel";
import { ProtectionPanel } from "./ProtectionPanel";
import { RecentBackups } from "./RecentBackups";
import { WebhooksPanel } from "./WebhooksPanel";
import { Button } from "./ui/Button";
import { LangSwitch } from "./ui/LangSwitch";

const NAV: { view: View; key: string; icon: LucideIcon }[] = [
  { view: "overview", key: "nav.overview", icon: LayoutDashboard },
  { view: "backups", key: "nav.backups", icon: History },
  { view: "notifications", key: "nav.notifications", icon: Bell },
];

export function Dashboard({ user }: { user: User }) {
  const qc = useQueryClient();
  const { t } = useI18n();
  const [view, setView] = useHashView();
  const navRef = useRef<HTMLElement>(null);

  // Slide the active-pill under the current nav item (and keep it there when
  // fonts load or the bar reflows).
  useLayoutEffect(() => {
    const nav = navRef.current;
    if (!nav) return;
    const place = () => {
      const el = nav.querySelector<HTMLElement>('[aria-current="page"]');
      if (!el) return;
      nav.style.setProperty("--pill-x", `${el.offsetLeft}px`);
      nav.style.setProperty("--pill-w", `${el.offsetWidth}px`);
    };
    place();
    const observer = new ResizeObserver(place);
    observer.observe(nav);
    return () => observer.disconnect();
  }, [view]);
  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      // Drop ALL authenticated cache immediately: a later 401 refetch must
      // not resurrect stale data (review P1-11).
      qc.setQueryData(["me"], null);
      qc.removeQueries();
    },
  });

  return (
    <div className="shell">
      <header className="topbar">
        <div className="topbar-inner">
          <Brand />
          <nav className="nav" aria-label={t("nav.sections")} ref={navRef}>
            {NAV.map(({ view: v, key, icon: Icon }) => (
              <button
                type="button"
                key={v}
                className="nav-item"
                aria-current={view === v ? "page" : undefined}
                onClick={() => setView(v)}
              >
                <Icon size={14} aria-hidden />
                <span>{t(key)}</span>
              </button>
            ))}
          </nav>
          <div className="topbar-user">
            <LangSwitch />
            <span className="muted truncate">{user.username}</span>
            <Button
              variant="ghost"
              size="sm"
              icon={LogOut}
              loading={logout.isPending}
              onClick={() => logout.mutate()}
            >
              {t("nav.signOut")}
            </Button>
          </div>
        </div>
      </header>

      <main className="page stagger" key={view}>
        {view === "overview" && (
          <>
            <HealthSummary />
            <ProtectionPanel />
            <PipelinePanel />
          </>
        )}
        {view === "backups" && <RecentBackups />}
        {view === "notifications" && (
          <>
            <WebhooksPanel />
            <DeliveryLog />
          </>
        )}
      </main>
    </div>
  );
}
