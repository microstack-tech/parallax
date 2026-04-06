import { jsx as _jsx, jsxs as _jsxs, Fragment as _Fragment } from "react/jsx-runtime";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api, openExternal } from "../lib/api";
import { formatBytes, formatDuration, formatLax, shortHex, timeAgo } from "../lib/format";
import SectionHeading from "../components/SectionHeading";
import StatusPill from "../components/StatusPill";
import AnimatedNumber from "../components/AnimatedNumber";
import { PageStagger, StaggerItem } from "../components/PageStagger";
const EXPLORER_URL = "https://explorer.parallaxprotocol.org";
export default function Dashboard() {
    const [status, setStatus] = useState(null);
    const [blocks, setBlocks] = useState([]);
    const [txs, setTxs] = useState([]);
    const [starting, setStarting] = useState(false);
    const [error, setError] = useState(null);
    // Track which block hashes / tx hashes are new since the last poll so we
    // can flash them gold for a second when they appear.
    const knownBlocks = useRef(new Set());
    const knownTxs = useRef(new Set());
    const headHash = useRef("");
    const [freshBlocks, setFreshBlocks] = useState(new Set());
    const [freshTxs, setFreshTxs] = useState(new Set());
    useEffect(() => {
        let alive = true;
        const refresh = async () => {
            try {
                const s = await api.nodeStatus();
                if (!alive)
                    return;
                // Skip re-renders when nothing visible to the user changed. We
                // compare the *formatted* uptime so that the once-per-2s
                // increment of uptimeSeconds doesn't force a full re-render of
                // the dashboard — it only re-renders when the displayed
                // duration string would actually flip.
                setStatus((prev) => {
                    if (prev &&
                        prev.running === s.running &&
                        prev.syncing === s.syncing &&
                        prev.currentBlock === s.currentBlock &&
                        prev.highestBlock === s.highestBlock &&
                        prev.peers === s.peers &&
                        formatDuration(prev.uptimeSeconds) ===
                            formatDuration(s.uptimeSeconds) &&
                        prev.diskUsedBytes === s.diskUsedBytes) {
                        return prev;
                    }
                    return s;
                });
                if (s.running) {
                    const [b, t] = await Promise.all([
                        api.recentBlocks(4).catch(() => []),
                        api.recentTransactions(6).catch(() => []),
                    ]);
                    if (!alive)
                        return;
                    // Only diff + re-render when the head block actually changed.
                    // Otherwise we'd re-render the entire blocks/txs tables every
                    // 2s for no reason, which churns React reconciliation.
                    const newHead = b.length > 0 ? b[0].hash : "";
                    if (newHead !== headHash.current) {
                        headHash.current = newHead;
                        const newBlocks = new Set();
                        for (const blk of b) {
                            if (knownBlocks.current.size > 0 && !knownBlocks.current.has(blk.hash)) {
                                newBlocks.add(blk.hash);
                            }
                            knownBlocks.current.add(blk.hash);
                        }
                        if (newBlocks.size)
                            setFreshBlocks(newBlocks);
                        const newTxs = new Set();
                        for (const tx of t) {
                            if (knownTxs.current.size > 0 && !knownTxs.current.has(tx.hash)) {
                                newTxs.add(tx.hash);
                            }
                            knownTxs.current.add(tx.hash);
                        }
                        if (newTxs.size)
                            setFreshTxs(newTxs);
                        setBlocks(b);
                        setTxs(t);
                    }
                }
                else {
                    setBlocks([]);
                    setTxs([]);
                }
            }
            catch {
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
        }
        catch (e) {
            setError(e?.message || String(e));
        }
        finally {
            setStarting(false);
        }
    };
    const stop = async () => {
        setError(null);
        try {
            await api.stopNode();
        }
        catch (e) {
            setError(e?.message || String(e));
        }
    };
    const syncPct = status && status.highestBlock > 0
        ? Math.min(100, Math.floor((status.currentBlock / status.highestBlock) * 100))
        : 0;
    const statusKind = !status?.running
        ? "stopped"
        : status.syncing
            ? "syncing"
            : "synced";
    return (_jsxs(PageStagger, { className: "space-y-14 max-w-5xl mx-auto", children: [status?.running && status.rpcEndpoint && (_jsx(StaggerItem, { children: _jsxs(Link, { to: "/connect", className: "group flex items-center justify-between gap-4 rounded border border-border bg-bg-elev/60 px-5 py-3 hover:border-fg/30 transition-colors", children: [_jsxs("div", { className: "flex items-center gap-3", children: [_jsx("span", { className: "live-dot-success" }), _jsxs("span", { className: "text-sm text-fg/80", children: ["Local RPC available at", " ", _jsx("span", { className: "font-mono text-fg", children: status.rpcEndpoint }), " ", "\u2014 connect MetaMask or any EVM wallet."] })] }), _jsx("span", { className: "link-arrow group-hover:gap-2", children: "Connect \u2192" })] }) })), _jsx(StaggerItem, { children: _jsx(SectionHeading, { eyebrow: "Client", title: "The Parallax Network.", trailing: !status?.running ? (_jsx("button", { className: "btn-primary", onClick: start, disabled: starting, children: starting ? "Starting…" : "Start node" })) : (_jsx("button", { className: "btn-ghost", onClick: stop, children: "Stop node" })) }) }), error && (_jsx(StaggerItem, { children: _jsx("div", { className: "card border-danger/40 bg-danger/10 text-danger", children: error }) })), _jsx(StaggerItem, { children: _jsxs("section", { className: "card-featured", children: [_jsxs("div", { className: "flex items-center justify-between mb-7", children: [_jsx("div", { className: "eyebrow", children: "Status" }), _jsx(StatusPill, { kind: statusKind })] }), status?.running ? (_jsxs(_Fragment, { children: [_jsxs("div", { className: "grid grid-cols-2 md:grid-cols-4 gap-8 mb-8", children: [_jsx(Stat, { label: "Block", children: _jsx(AnimatedNumber, { value: status.currentBlock, className: "stat-value" }) }), _jsx(Stat, { label: "Peers", children: _jsx(AnimatedNumber, { value: status.peers, className: "stat-value" }) }), _jsx(Stat, { label: "Uptime", children: _jsx("span", { className: "stat-value", children: formatDuration(status.uptimeSeconds) }) }), _jsx(Stat, { label: "Disk", children: _jsx("span", { className: "stat-value", children: formatBytes(status.diskUsedBytes) }) })] }), status.syncing && (_jsxs("div", { children: [_jsxs("div", { className: "flex justify-between eyebrow mb-3", children: [_jsx("span", { children: "Sync progress" }), _jsxs("span", { className: "font-mono normal-case tracking-normal text-fg", children: [status.currentBlock.toLocaleString(), " /", " ", status.highestBlock.toLocaleString(), " \u00B7 ", syncPct, "%"] })] }), _jsx("div", { className: "progress-track", children: _jsx("div", { className: "progress-fill", style: { width: `${syncPct}%` } }) })] }))] })) : (_jsxs("p", { className: "text-muted leading-relaxed", children: ["The node is stopped. Press", " ", _jsx("span", { className: "text-fg", children: "Start node" }), " to begin syncing the Parallax mainnet."] }))] }) }), status?.running && (_jsx(StaggerItem, { children: _jsxs("section", { className: "card", children: [_jsxs("div", { className: "flex items-center justify-between mb-6", children: [_jsx("div", { className: "eyebrow", children: "Latest blocks" }), _jsx("button", { type: "button", onClick: () => openExternal(EXPLORER_URL), className: "link-arrow", children: "View all on explorer \u2192" })] }), blocks.length === 0 ? (_jsx("p", { className: "text-muted text-sm", children: "No blocks yet \u2014 waiting for the chain to advance." })) : (_jsxs("table", { className: "w-full text-sm", children: [_jsx("thead", { children: _jsxs("tr", { className: "eyebrow text-left", children: [_jsx("th", { className: "font-medium pb-3", children: "Block" }), _jsx("th", { className: "font-medium pb-3", children: "Age" }), _jsx("th", { className: "font-medium pb-3", children: "Txs" }), _jsx("th", { className: "font-medium pb-3", children: "Gas used" }), _jsx("th", { className: "font-medium pb-3", children: "Reward" }), _jsx("th", { className: "font-medium pb-3", children: "Miner" }), _jsx("th", { className: "font-medium pb-3 text-right", children: "Size" })] }) }), _jsx("tbody", { className: "divide-y divide-border", children: blocks.map((b) => {
                                        const fillPct = b.gasLimit > 0
                                            ? Math.min(100, Math.round((b.gasUsed / b.gasLimit) * 100))
                                            : 0;
                                        const fresh = freshBlocks.has(b.hash);
                                        return (_jsxs("tr", { className: `text-fg/90 ${fresh ? "row-flash" : ""}`, children: [_jsxs("td", { className: "py-3", children: [_jsx("div", { className: "font-mono tabular-nums text-fg", children: b.number.toLocaleString() }), _jsx("div", { className: "font-mono text-[11px] text-muted", children: shortHex(b.hash, 8, 6) })] }), _jsx("td", { className: "py-3 text-muted", children: timeAgo(b.timestamp) }), _jsx("td", { className: "py-3 tabular-nums", children: b.txCount }), _jsxs("td", { className: "py-3", children: [_jsx("div", { className: "tabular-nums", children: b.gasUsed.toLocaleString() }), _jsxs("div", { className: "text-[11px] text-muted tabular-nums", children: [fillPct, "% full"] })] }), _jsxs("td", { className: "py-3 tabular-nums", children: [formatLax(b.rewardWei, 2), " ", _jsx("span", { className: "text-muted text-[11px]", children: "LAX" })] }), _jsx("td", { className: "py-3 font-mono text-[11px] text-muted", children: shortHex(b.coinbase, 6, 4) }), _jsx("td", { className: "py-3 text-right text-muted tabular-nums", children: formatBytes(b.sizeBytes) })] }, b.hash));
                                    }) })] }))] }) })), status?.running && (_jsx(StaggerItem, { children: _jsxs("section", { className: "card", children: [_jsxs("div", { className: "flex items-center justify-between mb-6", children: [_jsx("div", { className: "eyebrow", children: "Latest transactions" }), _jsx("button", { type: "button", onClick: () => openExternal(EXPLORER_URL), className: "link-arrow", children: "View all on explorer \u2192" })] }), txs.length === 0 ? (_jsx("p", { className: "text-muted text-sm", children: "No transactions in recent blocks." })) : (_jsxs("table", { className: "w-full text-sm", children: [_jsx("thead", { children: _jsxs("tr", { className: "eyebrow text-left", children: [_jsx("th", { className: "font-medium pb-3", children: "Hash" }), _jsx("th", { className: "font-medium pb-3", children: "Age" }), _jsx("th", { className: "font-medium pb-3", children: "From" }), _jsx("th", { className: "font-medium pb-3", children: "To" }), _jsx("th", { className: "font-medium pb-3 text-right", children: "Value" })] }) }), _jsx("tbody", { className: "divide-y divide-border", children: txs.map((t) => {
                                        const fresh = freshTxs.has(t.hash);
                                        return (_jsxs("tr", { className: `text-fg/90 ${fresh ? "row-flash" : ""}`, children: [_jsxs("td", { className: "py-3", children: [_jsx("div", { className: "font-mono text-[11px] text-fg", children: shortHex(t.hash, 10, 6) }), _jsxs("div", { className: "text-[11px] text-muted tabular-nums", children: ["block ", t.block.toLocaleString()] })] }), _jsx("td", { className: "py-3 text-muted", children: timeAgo(t.timestamp) }), _jsx("td", { className: "py-3 font-mono text-[11px] text-muted", children: shortHex(t.from, 6, 4) }), _jsx("td", { className: "py-3 font-mono text-[11px] text-muted", children: t.kind === "contract" ? (_jsx("span", { className: "pill-warn", children: "contract" })) : (shortHex(t.to, 6, 4)) }), _jsxs("td", { className: "py-3 text-right tabular-nums", children: [formatLax(t.valueWei, 4), " ", _jsx("span", { className: "text-muted text-[11px]", children: "LAX" })] })] }, t.hash));
                                    }) })] }))] }) }))] }));
}
function Stat({ label, children }) {
    return (_jsxs("div", { children: [_jsx("div", { className: "stat-label mb-2", children: label }), _jsx("div", { children: children })] }));
}
