import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { api, PeerView } from "../lib/api";
import SectionHeading from "../components/SectionHeading";
import { PageStagger, StaggerItem } from "../components/PageStagger";

type Filter = "all" | "in" | "out";

export default function Peers() {
  const [peers, setPeers] = useState<PeerView[]>([]);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [filter, setFilter] = useState<Filter>("all");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const refresh = async () => {
      try {
        const p = await api.peers();
        if (alive) setPeers(p);
      } catch (e: any) {
        if (alive) setError(e?.message || String(e));
      }
    };
    refresh();
    const id = setInterval(refresh, 2000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  const filtered = peers.filter((p) => {
    if (filter === "in") return p.inbound;
    if (filter === "out") return !p.inbound;
    return true;
  });

  const inCount = peers.filter((p) => p.inbound).length;
  const outCount = peers.length - inCount;

  return (
    <PageStagger className="space-y-12 max-w-5xl mx-auto">
      <StaggerItem>
        <SectionHeading
          eyebrow="Peers"
          title="All connected peers."
          subtitle={`${peers.length} total · ${inCount} inbound · ${outCount} outbound`}
        />
      </StaggerItem>

      {error && (
        <StaggerItem>
          <div className="card border-danger/40 bg-danger/10 text-danger">{error}</div>
        </StaggerItem>
      )}

      <StaggerItem>
        <section className="card">
          <div className="flex items-center justify-between mb-6">
            <div className="flex gap-2">
              {(["all", "in", "out"] as const).map((k) => (
                <button
                  key={k}
                  onClick={() => setFilter(k)}
                  className={`px-3.5 py-1.5 rounded text-[11px] uppercase tracking-wider transition-all duration-200 ${
                    filter === k
                      ? "bg-gold text-gold-fg shadow-gold-glow"
                      : "border border-border-strong text-muted hover:text-fg hover:border-fg/30"
                  }`}
                >
                  {k === "all" ? "All" : k === "in" ? "Inbound" : "Outbound"}
                </button>
              ))}
            </div>
            <div className="text-xs text-muted tabular-nums">
              Showing {filtered.length} of {peers.length}
            </div>
          </div>

          {filtered.length === 0 ? (
            <p className="text-muted text-sm py-6">No peers match this filter.</p>
          ) : (
            <ul className="divide-y divide-border">
              {filtered.map((p) => {
                const isExpanded = expanded === p.fullId;
                return (
                  <li key={p.fullId} className="py-4">
                    <button
                      type="button"
                      onClick={() => setExpanded(isExpanded ? null : p.fullId)}
                      className="w-full flex items-center gap-4 text-left group"
                    >
                      <span
                        className={p.inbound ? "pill-warn" : "pill-ok"}
                        title={p.inbound ? "Peer dialed us" : "We dialed peer"}
                      >
                        <span
                          className={
                            p.inbound ? "live-dot-warn" : "live-dot-success"
                          }
                        />
                        {p.inbound ? "IN" : "OUT"}
                      </span>
                      <div className="flex-1 min-w-0">
                        <div className="flex items-baseline gap-3">
                          <span className="font-mono text-xs text-fg">{p.id}</span>
                          <span className="text-sm text-fg/80 truncate group-hover:text-fg transition-colors">
                            {p.name || "unknown"}
                          </span>
                          {p.trusted && <span className="pill-ok">trusted</span>}
                          {p.static && <span className="pill-warn">static</span>}
                        </div>
                        <div className="font-mono text-[11px] text-muted truncate mt-0.5">
                          {p.remoteAddr} · {p.caps.join(", ")}
                        </div>
                      </div>
                      <motion.span
                        className="text-muted text-xs"
                        animate={{ rotate: isExpanded ? 90 : 0 }}
                        transition={{ duration: 0.3, ease: [0.16, 1, 0.3, 1] }}
                      >
                        ▸
                      </motion.span>
                    </button>

                    <AnimatePresence initial={false}>
                      {isExpanded && (
                        <motion.div
                          key="content"
                          initial={{ height: 0, opacity: 0 }}
                          animate={{ height: "auto", opacity: 1 }}
                          exit={{ height: 0, opacity: 0 }}
                          transition={{
                            duration: 0.4,
                            ease: [0.16, 1, 0.3, 1],
                          }}
                          className="overflow-hidden"
                        >
                          <div className="mt-4 ml-12 space-y-3 text-sm">
                            <Detail label="Full node ID" value={p.fullId} mono />
                            <Detail label="Enode URL" value={p.enode} mono wrap />
                            <Detail
                              label="Remote address"
                              value={p.remoteAddr}
                              mono
                            />
                            <Detail label="Local address" value={p.localAddr} mono />
                            <Detail
                              label="Capabilities"
                              value={p.caps.join(", ") || "none"}
                              mono
                            />
                            {p.protocols &&
                              Object.keys(p.protocols).length > 0 && (
                                <div>
                                  <div className="eyebrow mb-2">Protocols</div>
                                  <pre className="font-mono text-[11px] text-muted bg-bg-elev-2 rounded p-3 overflow-x-auto border border-border">
                                    {JSON.stringify(p.protocols, null, 2)}
                                  </pre>
                                </div>
                              )}
                          </div>
                        </motion.div>
                      )}
                    </AnimatePresence>
                  </li>
                );
              })}
            </ul>
          )}
        </section>
      </StaggerItem>
    </PageStagger>
  );
}

function Detail({
  label,
  value,
  mono,
  wrap,
}: {
  label: string;
  value: string;
  mono?: boolean;
  wrap?: boolean;
}) {
  return (
    <div className="grid grid-cols-[140px_1fr] gap-4 items-baseline">
      <div className="eyebrow">{label}</div>
      <div
        className={`text-fg/90 ${mono ? "font-mono text-xs" : ""} ${
          wrap ? "break-all" : "truncate"
        }`}
      >
        {value || "—"}
      </div>
    </div>
  );
}
