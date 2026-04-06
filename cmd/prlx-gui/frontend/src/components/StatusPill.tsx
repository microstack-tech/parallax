/**
 * StatusPill renders a state pill with an optional pulsing dot.
 *
 *   ● Syncing      (gold pulse)
 *   ● Synced       (green pulse)
 *   ● Stopped      (red, no pulse)
 *
 * The pulse uses a CSS animation (defined in tailwind.config.ts) — no JS
 * timer, so it stays in sync with the browser's compositor.
 */
export type StatusKind = "syncing" | "synced" | "stopped";

const VARIANTS: Record<
  StatusKind,
  { className: string; dot: string; label: string }
> = {
  syncing: {
    className: "pill-warn",
    dot: "live-dot-warn",
    label: "Syncing",
  },
  synced: {
    className: "pill-ok",
    dot: "live-dot-success",
    label: "Synced",
  },
  stopped: {
    className: "pill-err",
    dot: "live-dot-err",
    label: "Stopped",
  },
};

export default function StatusPill({
  kind,
  label,
}: {
  kind: StatusKind;
  label?: string;
}) {
  const v = VARIANTS[kind];
  return (
    <span className={v.className}>
      <span className={v.dot} />
      {label ?? v.label}
    </span>
  );
}
