import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { api } from "../lib/api";
import SectionHeading from "../components/SectionHeading";
import { PageStagger, StaggerItem } from "../components/PageStagger";
export default function Peers() {
    const [peers, setPeers] = useState([]);
    const [expanded, setExpanded] = useState(null);
    const [filter, setFilter] = useState("all");
    const [error, setError] = useState(null);
    useEffect(() => {
        let alive = true;
        const refresh = async () => {
            try {
                const p = await api.peers();
                if (alive)
                    setPeers(p);
            }
            catch (e) {
                if (alive)
                    setError(e?.message || String(e));
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
        if (filter === "in")
            return p.inbound;
        if (filter === "out")
            return !p.inbound;
        return true;
    });
    const inCount = peers.filter((p) => p.inbound).length;
    const outCount = peers.length - inCount;
    return (_jsxs(PageStagger, { className: "space-y-12 max-w-5xl mx-auto", children: [_jsx(StaggerItem, { children: _jsx(SectionHeading, { eyebrow: "Peers", title: "All connected peers.", subtitle: `${peers.length} total · ${inCount} inbound · ${outCount} outbound` }) }), error && (_jsx(StaggerItem, { children: _jsx("div", { className: "card border-danger/40 bg-danger/10 text-danger", children: error }) })), _jsx(StaggerItem, { children: _jsxs("section", { className: "card", children: [_jsxs("div", { className: "flex items-center justify-between mb-6", children: [_jsx("div", { className: "flex gap-2", children: ["all", "in", "out"].map((k) => (_jsx("button", { onClick: () => setFilter(k), className: `px-3.5 py-1.5 rounded text-[11px] uppercase tracking-wider transition-all duration-200 ${filter === k
                                            ? "bg-gold text-gold-fg shadow-gold-glow"
                                            : "border border-border-strong text-muted hover:text-fg hover:border-fg/30"}`, children: k === "all" ? "All" : k === "in" ? "Inbound" : "Outbound" }, k))) }), _jsxs("div", { className: "text-xs text-muted tabular-nums", children: ["Showing ", filtered.length, " of ", peers.length] })] }), filtered.length === 0 ? (_jsx("p", { className: "text-muted text-sm py-6", children: "No peers match this filter." })) : (_jsx("ul", { className: "divide-y divide-border", children: filtered.map((p) => {
                                const isExpanded = expanded === p.fullId;
                                return (_jsxs("li", { className: "py-4", children: [_jsxs("button", { type: "button", onClick: () => setExpanded(isExpanded ? null : p.fullId), className: "w-full flex items-center gap-4 text-left group", children: [_jsxs("span", { className: p.inbound ? "pill-warn" : "pill-ok", title: p.inbound ? "Peer dialed us" : "We dialed peer", children: [_jsx("span", { className: p.inbound ? "live-dot-warn" : "live-dot-success" }), p.inbound ? "IN" : "OUT"] }), _jsxs("div", { className: "flex-1 min-w-0", children: [_jsxs("div", { className: "flex items-baseline gap-3", children: [_jsx("span", { className: "font-mono text-xs text-fg", children: p.id }), _jsx("span", { className: "text-sm text-fg/80 truncate group-hover:text-fg transition-colors", children: p.name || "unknown" }), p.trusted && _jsx("span", { className: "pill-ok", children: "trusted" }), p.static && _jsx("span", { className: "pill-warn", children: "static" })] }), _jsxs("div", { className: "font-mono text-[11px] text-muted truncate mt-0.5", children: [p.remoteAddr, " \u00B7 ", p.caps.join(", ")] })] }), _jsx(motion.span, { className: "text-muted text-xs", animate: { rotate: isExpanded ? 90 : 0 }, transition: { duration: 0.3, ease: [0.16, 1, 0.3, 1] }, children: "\u25B8" })] }), _jsx(AnimatePresence, { initial: false, children: isExpanded && (_jsx(motion.div, { initial: { height: 0, opacity: 0 }, animate: { height: "auto", opacity: 1 }, exit: { height: 0, opacity: 0 }, transition: {
                                                    duration: 0.4,
                                                    ease: [0.16, 1, 0.3, 1],
                                                }, className: "overflow-hidden", children: _jsxs("div", { className: "mt-4 ml-12 space-y-3 text-sm", children: [_jsx(Detail, { label: "Full node ID", value: p.fullId, mono: true }), _jsx(Detail, { label: "Enode URL", value: p.enode, mono: true, wrap: true }), _jsx(Detail, { label: "Remote address", value: p.remoteAddr, mono: true }), _jsx(Detail, { label: "Local address", value: p.localAddr, mono: true }), _jsx(Detail, { label: "Capabilities", value: p.caps.join(", ") || "none", mono: true }), p.protocols &&
                                                            Object.keys(p.protocols).length > 0 && (_jsxs("div", { children: [_jsx("div", { className: "eyebrow mb-2", children: "Protocols" }), _jsx("pre", { className: "font-mono text-[11px] text-muted bg-bg-elev-2 rounded p-3 overflow-x-auto border border-border", children: JSON.stringify(p.protocols, null, 2) })] }))] }) }, "content")) })] }, p.fullId));
                            }) }))] }) })] }));
}
function Detail({ label, value, mono, wrap, }) {
    return (_jsxs("div", { className: "grid grid-cols-[140px_1fr] gap-4 items-baseline", children: [_jsx("div", { className: "eyebrow", children: label }), _jsx("div", { className: `text-fg/90 ${mono ? "font-mono text-xs" : ""} ${wrap ? "break-all" : "truncate"}`, children: value || "—" })] }));
}
