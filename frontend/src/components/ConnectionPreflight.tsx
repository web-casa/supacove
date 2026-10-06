import {
  CircleAlert,
  CircleCheck,
  Info,
  TriangleAlert,
  Wand2,
} from "lucide-react";
import { useI18n } from "../i18n";
import type { Analysis, Finding, HostPatch } from "../lib/connection";
import { Button } from "./ui/Button";

const LOOK: Record<Finding["level"], { tone: string; icon: typeof Info }> = {
  ok: { tone: "ok", icon: CircleCheck },
  info: { tone: "neutral", icon: Info },
  warn: { tone: "warn", icon: TriangleAlert },
  error: { tone: "danger", icon: CircleAlert },
};

interface Props {
  analysis: Analysis;
  onFix: (patch: HostPatch) => void;
}

/** What the pre-flight found, and exactly what will be sent (password masked). */
export function ConnectionPreflight({ analysis, onFix }: Props) {
  const { t } = useI18n();
  return (
    <div className="preflight" role="status" aria-label={t("preflight.aria")}>
      {analysis.redacted && (
        <p className="preflight-target">
          <span className="muted">{t("preflight.willRegister")}</span>
          <code>{analysis.redacted}</code>
        </p>
      )}
      <ul>
        {analysis.findings.map((f) => {
          const { tone, icon: Icon } = LOOK[f.level];
          return (
            <li className={`finding tone-${tone}`} key={f.key}>
              <Icon size={14} aria-hidden />
              <span>{t(f.key, f.vars)}</span>
              {f.fix && (
                <Button
                  size="sm"
                  icon={Wand2}
                  onClick={() => onFix(f.fix!.patch)}
                >
                  {t(f.fix.labelKey)}
                </Button>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
