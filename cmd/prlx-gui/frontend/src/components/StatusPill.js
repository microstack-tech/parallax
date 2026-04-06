import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
const VARIANTS = {
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
export default function StatusPill({ kind, label, }) {
    const v = VARIANTS[kind];
    return (_jsxs("span", { className: v.className, children: [_jsx("span", { className: v.dot }), label ?? v.label] }));
}
