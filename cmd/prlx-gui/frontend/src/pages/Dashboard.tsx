import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api, NodeStatus, BlockView, TxView, openExternal } from "../lib/api";
import { formatBytes, formatDuration, formatLax, shortHex, timeAgo } from "../lib/format";
import SectionHeading from "../components/SectionHeading";
import StatusPill, { StatusKind } from "../components/StatusPill";
import AnimatedNumber from "../components/AnimatedNumber";
import { PageStagger, StaggerItem } from "../components/PageStagger";

const EXPLORER_URL = "https://explorer.parallaxprotocol.org";

export default function Dashboard() {
  const [status, setStatus] = useState<NodeStatus | null>(null);
  const [blocks, setBlocks] = useState<BlockView[]>([]);
  const [txs, setTxs] = useState<TxView[]>([]);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Track which block hashes / tx hashes are new since the last poll so we
  // can flash them gold for a second when they appear.
  const knownBlocks = useRef<Set<string>>(new Set());
  const knownTxs = useRef<Set<string>>(new Set());
  const headHash = useRef<string>("");
  const [freshBlocks, setFreshBlocks] = useState<Set<string>>(new Set());
  const [freshTxs, setFreshTxs] = useState<Set<string>>(new Set());

  useEffect(() => {
    let alive = true;
    const refresh = async () => {
      try {
        const s = await api.nodeStatus();
        if (!alive) return;
        // Skip re-renders when nothing visible to the user changed. We
        // compare the *formatted* uptime so that the once-per-2s
        // increment of uptimeSeconds doesn't force a full re-render of
        // the dashboard — it only re-renders when the displayed
        // duration string would actually flip.
        setStatus((prev) => {
          if (
            prev &&
            prev.running === s.running &&
            prev.syncing === s.syncing &&
            prev.currentBlock === s.currentBlock &&
            prev.highestBlock === s.highestBlock &&
            prev.peers === s.peers &&
            formatDuration(prev.uptimeSeconds) ===
              formatDuration(s.uptimeSeconds) &&
            prev.diskUsedBytes === s.diskUsedBytes
          ) {
            return prev;
          }
          return s;
        });
        if (s.running) {
          const [b, t] = await Promise.all([
            api.recentBlocks(4).catch(() => [] as BlockView[]),
            api.recentTransactions(6).catch(() => [] as TxView[]),
          ]);
          if (!alive) return;

          // Only diff + re-render when the head block actually changed.
          // Otherwise we'd re-render the entire blocks/txs tables every
          // 2s for no reason, which churns React reconciliation.
          const newHead = b.length > 0 ? b[0].hash : "";
          if (newHead !== headHash.current) {
            headHash.current = newHead;
            const newBlocks = new Set<string>();
            for (const blk of b) {
              if (knownBlocks.current.size > 0 && !knownBlocks.current.has(blk.hash)) {
                newBlocks.add(blk.hash);
              }
              knownBlocks.current.add(blk.hash);
            }
            if (newBlocks.size) setFreshBlocks(newBlocks);

            const newTxs = new Set<string>();
            for (const tx of t) {
              if (knownTxs.current.size > 0 && !knownTxs.current.has(tx.hash)) {
                newTxs.add(tx.hash);
              }
              knownTxs.current.add(tx.hash);
            }
            if (newTxs.size) setFreshTxs(newTxs);

            setBlocks(b);
            setTxs(t);
          }
        } else {
          setBlocks([]);
          setTxs([]);
        }
      } catch {
        /* swallow — surfaced via action errors */
      }
    };
    refresh();
    const id = setInterval(refresh, 2000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  const start = async () => {
    setStarting(true);
    setError(null);
    try {
      await api.startNode();
    } catch (e: any) {
      setError(e?.message || String(e));
    } finally {
      setStarting(false);
    }
  };

  const stop = async () => {
    setError(null);
    try {
      await api.stopNode();
    } catch (e: any) {
      setError(e?.message || String(e));
    }
  };

  const syncPct =
    status && status.highestBlock > 0
      ? Math.min(100, Math.floor((status.currentBlock / status.highestBlock) * 100))
      : 0;

  const statusKind: StatusKind = !status?.running
    ? "stopped"
    : status.syncing
    ? "syncing"
    : "synced";

  return (
    <PageStagger className="space-y-14 max-w-5xl mx-auto">
      {status?.running && status.rpcEndpoint && (
        <StaggerItem>
          <Link
            to="/connect"
            className="group flex items-center justify-between gap-4 rounded border border-border bg-bg-elev/60 px-5 py-3 hover:border-fg/30 transition-colors"
          >
            <div className="flex items-center gap-3">
              <span className="live-dot-success" />
              <span className="text-sm text-fg/80">
                Local RPC available at{" "}
                <span className="font-mono text-fg">{status.rpcEndpoint}</span>{" "}
                — connect MetaMask or any EVM wallet.
              </span>
            </div>
            <span className="link-arrow group-hover:gap-2">Connect →</span>
          </Link>
        </StaggerItem>
      )}

      <StaggerItem>
        <SectionHeading
          eyebrow="Client"
          title="The Parallax Network."
          trailing={
            !status?.running ? (
              <button className="btn-primary" onClick={start} disabled={starting}>
                {starting ? "Starting…" : "Start node"}
              </button>
            ) : (
              <button className="btn-ghost" onClick={stop}>
                Stop node
              </button>
            )
          }
        />
      </StaggerItem>

      {error && (
        <StaggerItem>
          <div className="card border-danger/40 bg-danger/10 text-danger">{error}</div>
        </StaggerItem>
      )}

      {/* Featured card — node status */}
      <StaggerItem>
        <section className="card-featured">
          <div className="flex items-center justify-between mb-7">
            <div className="eyebrow">Status</div>
            <StatusPill kind={statusKind} />
          </div>
          {status?.running ? (
            <>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-8 mb-8">
                <Stat label="Block">
                  <AnimatedNumber value={status.currentBlock} className="stat-value" />
                </Stat>
                <Stat label="Peers">
                  <AnimatedNumber value={status.peers} className="stat-value" />
                </Stat>
                <Stat label="Uptime">
                  <span className="stat-value">
                    {formatDuration(status.uptimeSeconds)}
                  </span>
                </Stat>
                <Stat label="Disk">
                  <span className="stat-value">{formatBytes(status.diskUsedBytes)}</span>
                </Stat>
              </div>
              {status.syncing && (
                <div>
                  <div className="flex justify-between eyebrow mb-3">
                    <span>Sync progress</span>
                    <span className="font-mono normal-case tracking-normal text-fg">
                      {status.currentBlock.toLocaleString()} /{" "}
                      {status.highestBlock.toLocaleString()} · {syncPct}%
                    </span>
                  </div>
                  <div className="progress-track">
                    <div
                      className="progress-fill"
                      style={{ width: `${syncPct}%` }}
                    />
                  </div>
                </div>
              )}
            </>
          ) : (
            <p className="text-muted leading-relaxed">
              The node is stopped. Press{" "}
              <span className="text-fg">Start node</span> to begin syncing the
              Parallax mainnet.
            </p>
          )}
        </section>
      </StaggerItem>

      {/* Recent blocks */}
      {status?.running && (
        <StaggerItem>
          <section className="card">
            <div className="flex items-center justify-between mb-6">
              <div className="eyebrow">Latest blocks</div>
              <button
                type="button"
                onClick={() => openExternal(EXPLORER_URL)}
                className="link-arrow"
              >
                View all on explorer →
              </button>
            </div>
            {blocks.length === 0 ? (
              <p className="text-muted text-sm">
                No blocks yet — waiting for the chain to advance.
              </p>
            ) : (
              <table className="w-full text-sm">
                <thead>
                  <tr className="eyebrow text-left">
                    <th className="font-medium pb-3">Block</th>
                    <th className="font-medium pb-3">Age</th>
                    <th className="font-medium pb-3">Txs</th>
                    <th className="font-medium pb-3">Gas used</th>
                    <th className="font-medium pb-3">Reward</th>
                    <th className="font-medium pb-3">Miner</th>
                    <th className="font-medium pb-3 text-right">Size</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {blocks.map((b) => {
                    const fillPct =
                      b.gasLimit > 0
                        ? Math.min(
                            100,
                            Math.round((b.gasUsed / b.gasLimit) * 100),
                          )
                        : 0;
                    const fresh = freshBlocks.has(b.hash);
                    return (
                      <tr
                        key={b.hash}
                        className={`text-fg/90 ${fresh ? "row-flash" : ""}`}
                      >
                        <td className="py-3">
                          <div className="font-mono tabular-nums text-fg">
                            {b.number.toLocaleString()}
                          </div>
                          <div className="font-mono text-[11px] text-muted">
                            {shortHex(b.hash, 8, 6)}
                          </div>
                        </td>
                        <td className="py-3 text-muted">{timeAgo(b.timestamp)}</td>
                        <td className="py-3 tabular-nums">{b.txCount}</td>
                        <td className="py-3">
                          <div className="tabular-nums">
                            {b.gasUsed.toLocaleString()}
                          </div>
                          <div className="text-[11px] text-muted tabular-nums">
                            {fillPct}% full
                          </div>
                        </td>
                        <td className="py-3 tabular-nums">
                          {formatLax(b.rewardWei, 2)}{" "}
                          <span className="text-muted text-[11px]">LAX</span>
                        </td>
                        <td className="py-3 font-mono text-[11px] text-muted">
                          {shortHex(b.coinbase, 6, 4)}
                        </td>
                        <td className="py-3 text-right text-muted tabular-nums">
                          {formatBytes(b.sizeBytes)}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
          </section>
        </StaggerItem>
      )}

      {/* Recent transactions */}
      {status?.running && (
        <StaggerItem>
          <section className="card">
            <div className="flex items-center justify-between mb-6">
              <div className="eyebrow">Latest transactions</div>
              <button
                type="button"
                onClick={() => openExternal(EXPLORER_URL)}
                className="link-arrow"
              >
                View all on explorer →
              </button>
            </div>
            {txs.length === 0 ? (
              <p className="text-muted text-sm">
                No transactions in recent blocks.
              </p>
            ) : (
              <table className="w-full text-sm">
                <thead>
                  <tr className="eyebrow text-left">
                    <th className="font-medium pb-3">Hash</th>
                    <th className="font-medium pb-3">Age</th>
                    <th className="font-medium pb-3">From</th>
                    <th className="font-medium pb-3">To</th>
                    <th className="font-medium pb-3 text-right">Value</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {txs.map((t) => {
                    const fresh = freshTxs.has(t.hash);
                    return (
                      <tr
                        key={t.hash}
                        className={`text-fg/90 ${fresh ? "row-flash" : ""}`}
                      >
                        <td className="py-3">
                          <div className="font-mono text-[11px] text-fg">
                            {shortHex(t.hash, 10, 6)}
                          </div>
                          <div className="text-[11px] text-muted tabular-nums">
                            block {t.block.toLocaleString()}
                          </div>
                        </td>
                        <td className="py-3 text-muted">{timeAgo(t.timestamp)}</td>
                        <td className="py-3 font-mono text-[11px] text-muted">
                          {shortHex(t.from, 6, 4)}
                        </td>
                        <td className="py-3 font-mono text-[11px] text-muted">
                          {t.kind === "contract" ? (
                            <span className="pill-warn">contract</span>
                          ) : (
                            shortHex(t.to, 6, 4)
                          )}
                        </td>
                        <td className="py-3 text-right tabular-nums">
                          {formatLax(t.valueWei, 4)}{" "}
                          <span className="text-muted text-[11px]">LAX</span>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
          </section>
        </StaggerItem>
      )}
    </PageStagger>
  );
}

function Stat({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="stat-label mb-2">{label}</div>
      <div>{children}</div>
    </div>
  );
}
