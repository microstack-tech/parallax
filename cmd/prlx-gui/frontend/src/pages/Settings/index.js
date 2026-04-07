import { jsx as _jsx, jsxs as _jsxs, Fragment as _Fragment } from "react/jsx-runtime";
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { AnimatePresence, motion } from "motion/react";
import { api } from "../../lib/api";
import SectionHeading from "../../components/SectionHeading";
import { PageStagger, StaggerItem } from "../../components/PageStagger";
import Toggle from "../../components/Toggle";
// Fields that only take effect after a fresh node start. Touching any of
// these flips `restartPending` so we can warn the user in the save bar.
const RESTART_FIELDS = [
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
    const [cfg, setCfg] = useState(null);
    const [orig, setOrig] = useState(null);
    const [advanced, setAdvanced] = useState(false);
    const [saved, setSaved] = useState(false);
    const [err, setErr] = useState(null);
    // Sticks around even after Save until the user restarts the node.
    const [restartPending, setRestartPending] = useState(false);
    const [restarting, setRestarting] = useState(false);
    const [appVersion, setAppVersion] = useState("");
    const [clientVersion, setClientVersion] = useState("");
    useEffect(() => {
        api.getConfig().then((c) => {
            setCfg(c);
            setOrig(c);
        });
        api.version().then(setAppVersion).catch(() => { });
        api.clientVersion().then(setClientVersion).catch(() => { });
    }, []);
    if (!cfg)
        return null;
    const update = (patch) => setCfg({ ...cfg, ...patch });
    const dirty = !!orig && JSON.stringify(cfg) !== JSON.stringify(orig);
    // True when the *currently dirty* changes include any restart-only field.
    const dirtyNeedsRestart = !!orig && RESTART_FIELDS.some((k) => cfg[k] !== orig[k]);
    const save = async () => {
        setErr(null);
        try {
            await api.updateConfig(cfg);
            if (dirtyNeedsRestart)
                setRestartPending(true);
            setOrig(cfg);
            setSaved(true);
            setTimeout(() => setSaved(false), 2000);
        }
        catch (e) {
            setErr(e?.message || String(e));
        }
    };
    const discard = () => {
        if (orig)
            setCfg(orig);
        setErr(null);
    };
    const restartNode = async () => {
        setRestarting(true);
        setErr(null);
        try {
            await api.stopNode();
            await api.startNode();
            setRestartPending(false);
        }
        catch (e) {
            setErr(e?.message || String(e));
        }
        finally {
            setRestarting(false);
        }
    };
    return (_jsxs(PageStagger, { className: "space-y-12 max-w-3xl mx-auto", children: [_jsx(StaggerItem, { children: _jsx(SectionHeading, { eyebrow: "Settings", title: "Configuration." }) }), _jsx(StaggerItem, { children: _jsxs("section", { className: "card space-y-5", children: [_jsx("div", { className: "card-title", children: "Node" }), _jsx(Field, { label: "Data directory", children: _jsx("input", { className: "input font-mono", value: cfg.dataDir, onChange: (e) => update({ dataDir: e.target.value }) }) }), _jsx(Field, { label: "Sync mode", children: _jsxs("select", { className: "input", value: cfg.syncMode, onChange: (e) => update({ syncMode: e.target.value }), children: [_jsx("option", { value: "snap", children: "Snap (recommended)" }), _jsx("option", { value: "full", children: "Full" })] }) }), _jsx(Field, { label: "Max peers", children: _jsx("input", { type: "number", className: "input", value: cfg.maxPeers, onChange: (e) => update({ maxPeers: parseInt(e.target.value, 10) || 0 }) }) }), _jsx(Field, { label: "Auto-start node", children: _jsx(Toggle, { checked: cfg.autoStartNode, onChange: (v) => update({ autoStartNode: v }) }) })] }) }), _jsx(StaggerItem, { children: _jsxs("section", { className: "card space-y-5", children: [_jsx("div", { className: "card-title", children: "Networking" }), _jsx("p", { className: "text-sm text-muted leading-relaxed", children: "Allow other peers on the network to dial your node. When enabled, the client opens a UPnP/PMP port mapping on your router so peers can reach you. This makes the network healthier and gives you faster block propagation, but also means your IP becomes discoverable by other peers. Disable if you're behind a strict firewall or want to stay outbound-only." }), _jsx(Field, { label: "Allow inbound connections", children: _jsx(Toggle, { checked: !cfg.blockInbound, onChange: (v) => update({ blockInbound: !v }) }) })] }) }), _jsx(StaggerItem, { children: _jsxs("section", { className: "card space-y-5", children: [_jsx("div", { className: "card-title", children: "Local apps \u00B7 HTTP-RPC" }), _jsx("p", { className: "text-sm text-muted leading-relaxed", children: "Allow MetaMask and other local applications to connect to your node. The server is bound to 127.0.0.1 only \u2014 never expose it to the public internet." }), _jsx(Field, { label: "Enable HTTP-RPC", children: _jsx(Toggle, { checked: cfg.httpRpcEnabled, onChange: (v) => update({ httpRpcEnabled: v }) }) }), _jsx(AnimatePresence, { initial: false, children: cfg.httpRpcEnabled && (_jsx(motion.div, { initial: { height: 0, opacity: 0 }, animate: { height: "auto", opacity: 1 }, exit: { height: 0, opacity: 0 }, transition: { duration: 0.35, ease: [0.16, 1, 0.3, 1] }, className: "overflow-hidden", children: _jsx(Field, { label: "Port", children: _jsx("input", { type: "number", className: "input", value: cfg.httpRpcPort, onChange: (e) => update({
                                            httpRpcPort: parseInt(e.target.value, 10) || 8545,
                                        }) }) }) })) })] }) }), _jsx(StaggerItem, { children: _jsxs("section", { className: "card space-y-5", children: [_jsx("div", { className: "card-title", children: "Fee estimation" }), _jsxs("p", { className: "text-sm text-muted leading-relaxed", children: ["The gas price oracle has two algorithms. The default uses a percentile of recent block tips \u2014 fast and well-trodden. The Smart Fee option ports Bitcoin Core's", " ", _jsx("span", { className: "font-mono text-fg/80", children: "estimateSmartFee" }), " ", "algorithm, which buckets observed confirmations and back-solves for a fee that hits a target confirmation depth."] }), _jsxs("p", { className: "text-sm text-muted leading-relaxed", children: [_jsx("span", { className: "text-gold", children: "Recommended: leave this off." }), " ", "Smart Fee needs a long, continuous window of observed confirmations to produce accurate estimates. Parallax Desktop is designed to be started and stopped on demand rather than left running 24/7, so the oracle rarely accumulates enough data and falls back to the default minimum. Always-on operators (CLI nodes, validators) are the intended audience for this option."] }), _jsx(Field, { label: "Smart fee estimator", children: _jsx(Toggle, { checked: cfg.enableSmartFee, onChange: (v) => update({ enableSmartFee: v }) }) })] }) }), _jsx(StaggerItem, { children: _jsxs("section", { className: "card", children: [_jsx("div", { className: "card-title mb-4", children: "Diagnostics" }), _jsxs("div", { className: "flex items-center justify-between gap-6", children: [_jsx("p", { className: "text-sm text-muted leading-relaxed max-w-md", children: "View the live tail of node and GUI logs. Useful for debugging sync issues, peer discovery, or filing bug reports." }), _jsx(Link, { to: "/logs", className: "btn-ghost shrink-0", children: "Show logs" })] })] }) }), _jsx(StaggerItem, { children: _jsxs("section", { className: "card space-y-3", children: [_jsxs("button", { className: "btn-ghost", onClick: () => setAdvanced(!advanced), children: [advanced ? "Hide" : "Show", " advanced settings"] }), _jsx(AnimatePresence, { initial: false, children: advanced && (_jsx(motion.div, { initial: { height: 0, opacity: 0 }, animate: { height: "auto", opacity: 1 }, exit: { height: 0, opacity: 0 }, transition: { duration: 0.4, ease: [0.16, 1, 0.3, 1] }, className: "overflow-hidden", children: _jsxs("div", { className: "space-y-5 pt-4", children: [_jsx(Field, { label: "Database cache (MB)", children: _jsx("input", { type: "number", className: "input", value: cfg.databaseCacheMB, onChange: (e) => update({
                                                    databaseCacheMB: parseInt(e.target.value, 10) || 0,
                                                }) }) }), _jsx(Field, { label: "Trie clean cache (MB)", children: _jsx("input", { type: "number", className: "input", value: cfg.trieCleanCacheMB, onChange: (e) => update({
                                                    trieCleanCacheMB: parseInt(e.target.value, 10) || 0,
                                                }) }) }), _jsx(Field, { label: "Trie dirty cache (MB)", children: _jsx("input", { type: "number", className: "input", value: cfg.trieDirtyCacheMB, onChange: (e) => update({
                                                    trieDirtyCacheMB: parseInt(e.target.value, 10) || 0,
                                                }) }) }), _jsx(Field, { label: "Snapshot cache (MB)", children: _jsx("input", { type: "number", className: "input", value: cfg.snapshotCacheMB, onChange: (e) => update({
                                                    snapshotCacheMB: parseInt(e.target.value, 10) || 0,
                                                }) }) })] }) })) })] }) }), _jsx(StaggerItem, { children: _jsxs("section", { className: "card", children: [_jsx("div", { className: "card-title mb-4", children: "About" }), _jsxs("dl", { className: "grid grid-cols-3 gap-y-3 text-sm", children: [_jsx("dt", { className: "eyebrow self-center", children: "Client" }), _jsxs("dd", { className: "col-span-2 font-mono text-fg", children: ["prlx ", clientVersion || "—"] }), _jsx("dt", { className: "eyebrow self-center", children: "Desktop" }), _jsx("dd", { className: "col-span-2 font-mono text-fg", children: appVersion || "—" })] })] }) }), err && (_jsx(StaggerItem, { children: _jsx("div", { className: "card border-danger/40 bg-danger/10 text-danger", children: err }) })), _jsx("div", { className: "h-20" }), _jsx(FloatingSaveBar, { dirty: dirty, saved: saved, dirtyNeedsRestart: dirtyNeedsRestart, restartPending: restartPending, restarting: restarting, onSave: save, onDiscard: discard, onRestart: restartNode })] }));
}
function FloatingSaveBar({ dirty, saved, dirtyNeedsRestart, restartPending, restarting, onSave, onDiscard, onRestart, }) {
    const visible = dirty || saved || restartPending;
    return (_jsx(AnimatePresence, { children: visible && (_jsx(motion.div, { className: "fixed bottom-8 right-8 z-40 flex items-center gap-3 rounded-full border border-border-strong bg-bg-elev/95 backdrop-blur px-4 py-2.5 shadow-2xl max-w-[90vw]", initial: { opacity: 0, y: 20 }, animate: { opacity: 1, y: 0 }, exit: { opacity: 0, y: 20 }, transition: { duration: 0.25, ease: [0.16, 1, 0.3, 1] }, children: dirty ? (_jsxs(_Fragment, { children: [_jsxs("div", { className: "flex flex-col pl-1 pr-1 leading-tight", children: [_jsx("span", { className: "text-xs text-fg", children: "Unsaved changes" }), dirtyNeedsRestart && (_jsx("span", { className: "text-[10px] uppercase tracking-wider text-gold", children: "Will require node restart" }))] }), _jsx("button", { type: "button", onClick: onDiscard, className: "text-[11px] uppercase tracking-wider text-muted hover:text-fg transition-colors px-2", children: "Discard" }), _jsx("button", { type: "button", onClick: onSave, className: "btn-primary !py-1.5 !px-4", children: "Save" })] })) : restartPending ? (_jsxs(_Fragment, { children: [_jsxs("div", { className: "flex items-center gap-2 pl-1 pr-1", children: [_jsx("span", { className: "live-dot-warn" }), _jsx("span", { className: "text-xs text-fg", children: "Restart required to apply changes" })] }), _jsx("button", { type: "button", onClick: onRestart, disabled: restarting, className: "btn-primary !py-1.5 !px-4", children: restarting ? "Restarting…" : "Restart node" })] })) : (_jsxs("span", { className: "text-success text-sm flex items-center gap-2 px-2", children: [_jsx("span", { className: "live-dot-success" }), "Saved"] })) }, "save-bar")) }));
}
function Field({ label, children }) {
    return (_jsxs("div", { className: "grid grid-cols-3 gap-4 items-center", children: [_jsx("label", { className: "eyebrow", children: label }), _jsx("div", { className: "col-span-2", children: children })] }));
}
