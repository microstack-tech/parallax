import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { motion } from "motion/react";
import { api } from "../lib/api";
import SectionHeading from "../components/SectionHeading";
import { PageStagger, StaggerItem } from "../components/PageStagger";
export default function Logs() {
    const [lines, setLines] = useState([]);
    const [filter, setFilter] = useState("");
    const [paused, setPaused] = useState(false);
    const ref = useRef(null);
    useEffect(() => {
        api.getLogTail(500).then(setLines);
        const off = window.runtime?.EventsOn?.("log", (line) => {
            setLines((prev) => {
                const next = [...prev, line];
                return next.length > 2000 ? next.slice(next.length - 2000) : next;
            });
        });
        return () => {
            if (typeof off === "function")
                off();
        };
    }, []);
    useEffect(() => {
        if (paused)
            return;
        if (ref.current)
            ref.current.scrollTop = ref.current.scrollHeight;
    }, [lines, paused]);
    const visible = filter
        ? lines.filter((l) => l.msg.toLowerCase().includes(filter.toLowerCase()))
        : lines;
    return (_jsxs(PageStagger, { className: "space-y-10 max-w-6xl mx-auto h-full flex flex-col", children: [_jsx(StaggerItem, { children: _jsx(SectionHeading, { eyebrow: "Logs", title: "Live tail.", subtitle: "Streaming directly from the embedded node, level INFO and above.", trailing: _jsxs("div", { className: "flex items-center gap-4", children: [_jsxs("span", { className: "flex items-center gap-2 text-xs text-muted", children: [_jsx("span", { className: paused ? "live-dot bg-muted" : "live-dot-success" }), paused ? "Paused" : "Live"] }), _jsx(Link, { to: "/settings", className: "btn-ghost", children: "\u2190 Back" })] }) }) }), _jsxs(StaggerItem, { className: "flex-1 flex flex-col min-h-0", children: [_jsxs("div", { className: "flex gap-2 mb-4", children: [_jsx("input", { className: "input flex-1", placeholder: "Filter\u2026", value: filter, onChange: (e) => setFilter(e.target.value) }), _jsx("button", { className: "btn-ghost", onClick: () => setPaused((p) => !p), children: paused ? "Resume" : "Pause" }), _jsx("button", { className: "btn-ghost", onClick: () => navigator.clipboard.writeText(visible.map((l) => l.msg).join("\n")), children: "Copy" })] }), _jsx(motion.div, { ref: ref, className: "card flex-1 min-h-0 overflow-y-auto font-mono text-xs whitespace-pre-wrap leading-relaxed", initial: { opacity: 0 }, animate: { opacity: 1 }, transition: { duration: 0.5 }, children: visible.length === 0 ? (_jsx("p", { className: "text-muted not-italic", children: "No log lines yet." })) : (visible.map((l, i) => (_jsxs("div", { className: "grid grid-cols-[60px_1fr] gap-3 py-0.5", children: [_jsx("span", { className: "text-muted", children: l.level.trim() }), _jsx("span", { className: "text-fg/85", children: l.msg })] }, i)))) })] })] }));
}
