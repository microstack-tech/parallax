import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { useEffect, useState } from "react";
import { api } from "../lib/api";
const VARIANTS = {
    synced: { dot: "live-dot-success", label: "Synced" },
    syncing: { dot: "live-dot-warn", label: "Syncing" },
    stopped: { dot: "live-dot-err", label: "Stopped" },
};
export default function ClientStatus() {
    const [kind, setKind] = useState("stopped");
    useEffect(() => {
        let alive = true;
        const refresh = async () => {
            try {
                const s = await api.nodeStatus();
                if (!alive)
                    return;
                const next = !s.running
                    ? "stopped"
                    : s.syncing
                        ? "syncing"
                        : "synced";
                setKind((prev) => (prev === next ? prev : next));
            }
            catch {
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
    return (_jsxs("div", { className: "flex items-center gap-2.5", children: [_jsx("span", { className: v.dot }), _jsx("span", { className: "eyebrow", children: v.label })] }));
}
