import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { AnimatePresence, motion } from "motion/react";
import { api, GUIConfig } from "../../lib/api";
import SectionHeading from "../../components/SectionHeading";
import { PageStagger, StaggerItem } from "../../components/PageStagger";
import Toggle from "../../components/Toggle";

// Fields that only take effect after a fresh node start. Touching any of
// these flips `restartPending` so we can warn the user in the save bar.
const RESTART_FIELDS: (keyof GUIConfig)[] = [
  "dataDir",
  "syncMode",
  "httpRpcEnabled",
  "httpRpcPort",
  "blockInbound",
  "maxPeers",
  "enableSmartFee",
  "databaseCacheMB",
  "trieCleanCacheMB",
  "trieDirtyCacheMB",
  "snapshotCacheMB",
];

export default function Settings() {
  const [cfg, setCfg] = useState<GUIConfig | null>(null);
  const [orig, setOrig] = useState<GUIConfig | null>(null);
  const [advanced, setAdvanced] = useState(false);
  const [saved, setSaved] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  // Sticks around even after Save until the user restarts the node.
  const [restartPending, setRestartPending] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [appVersion, setAppVersion] = useState<string>("");
  const [clientVersion, setClientVersion] = useState<string>("");

  useEffect(() => {
    api.getConfig().then((c) => {
      setCfg(c);
      setOrig(c);
    });
    api.version().then(setAppVersion).catch(() => { });
    api.clientVersion().then(setClientVersion).catch(() => { });
  }, []);

  if (!cfg) return null;

  const update = (patch: Partial<GUIConfig>) => setCfg({ ...cfg, ...patch });

  const dirty =
    !!orig && JSON.stringify(cfg) !== JSON.stringify(orig);

  // True when the *currently dirty* changes include any restart-only field.
  const dirtyNeedsRestart =
    !!orig && RESTART_FIELDS.some((k) => cfg[k] !== orig[k]);

  const save = async () => {
    setErr(null);
    try {
      await api.updateConfig(cfg);
      if (dirtyNeedsRestart) setRestartPending(true);
      setOrig(cfg);
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
    } catch (e: any) {
      setErr(e?.message || String(e));
    }
  };

  const discard = () => {
    if (orig) setCfg(orig);
    setErr(null);
  };

  const restartNode = async () => {
    setRestarting(true);
    setErr(null);
    try {
      await api.stopNode();
      await api.startNode();
      setRestartPending(false);
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setRestarting(false);
    }
  };

  return (
    <PageStagger className="space-y-12 max-w-3xl mx-auto">
      <StaggerItem>
        <SectionHeading
          eyebrow="Settings"
          title="Configuration."
        />
      </StaggerItem>

      <StaggerItem>
        <section className="card space-y-5">
          <div className="card-title">Node</div>

          <Field label="Data directory">
            <input
              className="input font-mono"
              value={cfg.dataDir}
              onChange={(e) => update({ dataDir: e.target.value })}
            />
          </Field>

          <Field label="Sync mode">
            <select
              className="input"
              value={cfg.syncMode}
              onChange={(e) =>
                update({ syncMode: e.target.value as GUIConfig["syncMode"] })
              }
            >
              <option value="snap">Snap (recommended)</option>
              <option value="full">Full</option>
            </select>
          </Field>

          <Field label="Max peers">
            <input
              type="number"
              className="input"
              value={cfg.maxPeers}
              onChange={(e) =>
                update({ maxPeers: parseInt(e.target.value, 10) || 0 })
              }
            />
          </Field>

          <Field label="Auto-start node">
            <Toggle
              checked={cfg.autoStartNode}
              onChange={(v) => update({ autoStartNode: v })}
            />
          </Field>
        </section>
      </StaggerItem>

      <StaggerItem>
        <section className="card space-y-5">
          <div className="card-title">Networking</div>
          <p className="text-sm text-muted leading-relaxed">
            Allow other peers on the network to dial your node. When enabled,
            the client opens a UPnP/PMP port mapping on your router so peers
            can reach you. This makes the network healthier and gives you
            faster block propagation, but also means your IP becomes
            discoverable by other peers. Disable if you're behind a strict
            firewall or want to stay outbound-only.
          </p>

          <Field label="Allow inbound connections">
            <Toggle
              checked={!cfg.blockInbound}
              onChange={(v) => update({ blockInbound: !v })}
            />
          </Field>
        </section>
      </StaggerItem>

      <StaggerItem>
        <section className="card space-y-5">
          <div className="card-title">Local apps · HTTP-RPC</div>
          <p className="text-sm text-muted leading-relaxed">
            Allow MetaMask and other local applications to connect to your node.
            The server is bound to 127.0.0.1 only — never expose it to the public
            internet.
          </p>

          <Field label="Enable HTTP-RPC">
            <Toggle
              checked={cfg.httpRpcEnabled}
              onChange={(v) => update({ httpRpcEnabled: v })}
            />
          </Field>
          <AnimatePresence initial={false}>
            {cfg.httpRpcEnabled && (
              <motion.div
                initial={{ height: 0, opacity: 0 }}
                animate={{ height: "auto", opacity: 1 }}
                exit={{ height: 0, opacity: 0 }}
                transition={{ duration: 0.35, ease: [0.16, 1, 0.3, 1] }}
                className="overflow-hidden"
              >
                <Field label="Port">
                  <input
                    type="number"
                    className="input"
                    value={cfg.httpRpcPort}
                    onChange={(e) =>
                      update({
                        httpRpcPort: parseInt(e.target.value, 10) || 8545,
                      })
                    }
                  />
                </Field>
              </motion.div>
            )}
          </AnimatePresence>
        </section>
      </StaggerItem>

      <StaggerItem>
        <section className="card space-y-5">
          <div className="card-title">Fee estimation</div>
          <p className="text-sm text-muted leading-relaxed">
            The gas price oracle has two algorithms. The default uses a
            percentile of recent block tips — fast and well-trodden. The
            Smart Fee option ports Bitcoin Core's{" "}
            <span className="font-mono text-fg/80">estimateSmartFee</span>{" "}
            algorithm, which buckets observed confirmations and back-solves
            for a fee that hits a target confirmation depth.
          </p>
          <p className="text-sm text-muted leading-relaxed">
            <span className="text-gold">Recommended: leave this off.</span>{" "}
            Smart Fee needs a long, continuous window of observed
            confirmations to produce accurate estimates. Parallax Desktop is
            designed to be started and stopped on demand rather than left
            running 24/7, so the oracle rarely accumulates enough data and
            falls back to the default minimum. Always-on operators (CLI
            nodes, validators) are the intended audience for this option.
          </p>

          <Field label="Smart fee estimator">
            <Toggle
              checked={cfg.enableSmartFee}
              onChange={(v) => update({ enableSmartFee: v })}
            />
          </Field>
        </section>
      </StaggerItem>

      <StaggerItem>
        <section className="card">
          <div className="card-title mb-4">Diagnostics</div>
          <div className="flex items-center justify-between gap-6">
            <p className="text-sm text-muted leading-relaxed max-w-md">
              View the live tail of node and GUI logs. Useful for debugging
              sync issues, peer discovery, or filing bug reports.
            </p>
            <Link to="/logs" className="btn-ghost shrink-0">
              Show logs
            </Link>
          </div>
        </section>
      </StaggerItem>

      <StaggerItem>
        <section className="card space-y-3">
          <button
            className="btn-ghost"
            onClick={() => setAdvanced(!advanced)}
          >
            {advanced ? "Hide" : "Show"} advanced settings
          </button>

          <AnimatePresence initial={false}>
            {advanced && (
              <motion.div
                initial={{ height: 0, opacity: 0 }}
                animate={{ height: "auto", opacity: 1 }}
                exit={{ height: 0, opacity: 0 }}
                transition={{ duration: 0.4, ease: [0.16, 1, 0.3, 1] }}
                className="overflow-hidden"
              >
                <div className="space-y-5 pt-4">
                  <Field label="Database cache (MB)">
                    <input
                      type="number"
                      className="input"
                      value={cfg.databaseCacheMB}
                      onChange={(e) =>
                        update({
                          databaseCacheMB: parseInt(e.target.value, 10) || 0,
                        })
                      }
                    />
                  </Field>
                  <Field label="Trie clean cache (MB)">
                    <input
                      type="number"
                      className="input"
                      value={cfg.trieCleanCacheMB}
                      onChange={(e) =>
                        update({
                          trieCleanCacheMB: parseInt(e.target.value, 10) || 0,
                        })
                      }
                    />
                  </Field>
                  <Field label="Trie dirty cache (MB)">
                    <input
                      type="number"
                      className="input"
                      value={cfg.trieDirtyCacheMB}
                      onChange={(e) =>
                        update({
                          trieDirtyCacheMB: parseInt(e.target.value, 10) || 0,
                        })
                      }
                    />
                  </Field>
                  <Field label="Snapshot cache (MB)">
                    <input
                      type="number"
                      className="input"
                      value={cfg.snapshotCacheMB}
                      onChange={(e) =>
                        update({
                          snapshotCacheMB: parseInt(e.target.value, 10) || 0,
                        })
                      }
                    />
                  </Field>
                </div>
              </motion.div>
            )}
          </AnimatePresence>
        </section>
      </StaggerItem>

      <StaggerItem>
        <section className="card">
          <div className="card-title mb-4">About</div>
          <dl className="grid grid-cols-3 gap-y-3 text-sm">
            <dt className="eyebrow self-center">Client</dt>
            <dd className="col-span-2 font-mono text-fg">
              prlx {clientVersion || "—"}
            </dd>
            <dt className="eyebrow self-center">Desktop</dt>
            <dd className="col-span-2 font-mono text-fg">
              {appVersion || "—"}
            </dd>
          </dl>
        </section>
      </StaggerItem>

      {err && (
        <StaggerItem>
          <div className="card border-danger/40 bg-danger/10 text-danger">{err}</div>
        </StaggerItem>
      )}

      {/* Spacer so the floating save bar never covers the last setting. */}
      <div className="h-20" />

      <FloatingSaveBar
        dirty={dirty}
        saved={saved}
        dirtyNeedsRestart={dirtyNeedsRestart}
        restartPending={restartPending}
        restarting={restarting}
        onSave={save}
        onDiscard={discard}
        onRestart={restartNode}
      />
    </PageStagger>
  );
}

function FloatingSaveBar({
  dirty,
  saved,
  dirtyNeedsRestart,
  restartPending,
  restarting,
  onSave,
  onDiscard,
  onRestart,
}: {
  dirty: boolean;
  saved: boolean;
  dirtyNeedsRestart: boolean;
  restartPending: boolean;
  restarting: boolean;
  onSave: () => void;
  onDiscard: () => void;
  onRestart: () => void;
}) {
  const visible = dirty || saved || restartPending;
  return (
    <AnimatePresence>
      {visible && (
        <motion.div
          key="save-bar"
          className="fixed bottom-8 right-8 z-40 flex items-center gap-3 rounded-full border border-border-strong bg-bg-elev/95 backdrop-blur px-4 py-2.5 shadow-2xl max-w-[90vw]"
          initial={{ opacity: 0, y: 20 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, y: 20 }}
          transition={{ duration: 0.25, ease: [0.16, 1, 0.3, 1] }}
        >
          {dirty ? (
            <>
              <div className="flex flex-col pl-1 pr-1 leading-tight">
                <span className="text-xs text-fg">Unsaved changes</span>
                {dirtyNeedsRestart && (
                  <span className="text-[10px] uppercase tracking-wider text-gold">
                    Will require node restart
                  </span>
                )}
              </div>
              <button
                type="button"
                onClick={onDiscard}
                className="text-[11px] uppercase tracking-wider text-muted hover:text-fg transition-colors px-2"
              >
                Discard
              </button>
              <button
                type="button"
                onClick={onSave}
                className="btn-primary !py-1.5 !px-4"
              >
                Save
              </button>
            </>
          ) : restartPending ? (
            <>
              <div className="flex items-center gap-2 pl-1 pr-1">
                <span className="live-dot-warn" />
                <span className="text-xs text-fg">
                  Restart required to apply changes
                </span>
              </div>
              <button
                type="button"
                onClick={onRestart}
                disabled={restarting}
                className="btn-primary !py-1.5 !px-4"
              >
                {restarting ? "Restarting…" : "Restart node"}
              </button>
            </>
          ) : (
            <span className="text-success text-sm flex items-center gap-2 px-2">
              <span className="live-dot-success" />
              Saved
            </span>
          )}
        </motion.div>
      )}
    </AnimatePresence>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-3 gap-4 items-center">
      <label className="eyebrow">{label}</label>
      <div className="col-span-2">{children}</div>
    </div>
  );
}

