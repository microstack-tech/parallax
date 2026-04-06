import { useEffect, useState } from "react";
import { api } from "../lib/api";

/**
 * ClientStatus is the live client-state indicator that lives in the TopBar.
 *
 * It polls node status on its own short interval (independent of any page)
 * so the badge stays accurate as you navigate around the app. Three states:
 *
 *   ● Synced     (green pulse)
 *   ● Syncing    (gold pulse)
 *   ● Stopped    (red, no pulse)
 */
type Kind = "synced" | "syncing" | "stopped";

const VARIANTS: Record<Kind, { dot: string; label: string }> = {
  synced: { dot: "live-dot-success", label: "Synced" },
  syncing: { dot: "live-dot-warn", label: "Syncing" },
  stopped: { dot: "live-dot-err", label: "Stopped" },
};

export default function ClientStatus() {
  const [kind, setKind] = useState<Kind>("stopped");

  useEffect(() => {
    let alive = true;
    const refresh = async () => {
      try {
        const s = await api.nodeStatus();
        if (!alive) return;
        const next: Kind = !s.running
          ? "stopped"
          : s.syncing
          ? "syncing"
          : "synced";
        setKind((prev) => (prev === next ? prev : next));
      } catch {
        /* swallow — the dashboard surfaces real errors */
      }
    };
    refresh();
    const id = setInterval(refresh, 3000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  const v = VARIANTS[kind];
  return (
    <div className="flex items-center gap-2.5">
      <span className={v.dot} />
      <span className="eyebrow">{v.label}</span>
    </div>
  );
}
