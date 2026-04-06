import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { api } from "../../lib/api";
import Toggle from "../../components/Toggle";
import logo from "../../assets/logo.svg";
const STEPS = [
    "welcome",
    "datadir",
    "syncmode",
    "networking",
    "rpc",
    "starting",
];
export default function Onboarding({ onFinished }) {
    const [cfg, setCfg] = useState(null);
    const [step, setStep] = useState("welcome");
    const [error, setError] = useState(null);
    useEffect(() => {
        api.getConfig().then(setCfg);
    }, []);
    if (!cfg)
        return null;
    const update = (patch) => setCfg({ ...cfg, ...patch });
    const finish = async () => {
        setStep("starting");
        setError(null);
        try {
            await api.saveBootstrap(cfg);
            await api.startNode();
            onFinished();
        }
        catch (e) {
            setError(e?.message || String(e));
            setStep("rpc");
        }
    };
    const stepIndex = STEPS.indexOf(step);
    return (_jsxs("div", { className: "h-full grid place-items-center p-8 relative overflow-hidden", children: [_jsx("div", { className: "absolute inset-0 pointer-events-none", children: _jsx("div", { className: "absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 h-[600px] w-[600px] rounded-full", style: {
                        background: "radial-gradient(circle, rgb(247 147 26 / 0.10), transparent 60%)",
                    } }) }), _jsxs("div", { className: "w-full max-w-2xl relative", children: [_jsxs(motion.div, { className: "flex flex-col items-center gap-4 mb-12", initial: { opacity: 0, y: 16 }, animate: { opacity: 1, y: 0 }, transition: { duration: 0.7, ease: [0.16, 1, 0.3, 1] }, children: [_jsx(motion.img, { src: logo, className: "h-16 w-16", alt: "Parallax", initial: { scale: 0.8, opacity: 0 }, animate: { scale: 1, opacity: 1 }, transition: { duration: 0.8, ease: [0.16, 1, 0.3, 1] } }), _jsx(motion.span, { className: "block h-[2px] w-10 bg-gold rounded-full", initial: { scaleX: 0 }, animate: { scaleX: 1 }, transition: { duration: 0.8, delay: 0.3, ease: [0.16, 1, 0.3, 1] } }), _jsx("div", { className: "eyebrow", children: "Welcome" }), _jsx("h1", { className: "display text-center", children: "Parallax Desktop." })] }), step !== "starting" && (_jsx("div", { className: "flex justify-center gap-2 mb-6", children: STEPS.slice(0, -1).map((s, i) => (_jsx(motion.span, { className: "h-1 rounded-full", animate: {
                                width: i === stepIndex ? 24 : 8,
                                backgroundColor: i <= stepIndex
                                    ? "rgb(247 147 26)"
                                    : "oklch(1 0 0 / 0.15)",
                            }, transition: { duration: 0.4, ease: [0.16, 1, 0.3, 1] } }, s))) })), error && (_jsx(motion.div, { className: "card border-danger/40 bg-danger/10 text-danger mb-4", initial: { opacity: 0, y: 8 }, animate: { opacity: 1, y: 0 }, children: error })), _jsx(AnimatePresence, { mode: "wait", children: _jsxs(motion.div, { initial: { opacity: 0, y: 16 }, animate: { opacity: 1, y: 0 }, exit: { opacity: 0, y: -12 }, transition: { duration: 0.4, ease: [0.16, 1, 0.3, 1] }, children: [step === "welcome" && (_jsxs("div", { className: "card space-y-5", children: [_jsx("h2", { className: "display-sm", children: "Run your own Parallax node" }), _jsx("p", { className: "text-muted leading-relaxed", children: "Parallax Desktop is a self-contained Parallax full node with a friendly UI. The wizard will set up your data directory and connect you to the network. Once running, you can also point MetaMask or any other EVM-compatible wallet at your local node." }), _jsx("button", { className: "btn-primary", onClick: () => setStep("datadir"), children: "Get started" })] })), step === "datadir" && (_jsxs("div", { className: "card space-y-5", children: [_jsx("h2", { className: "display-sm", children: "Where should we keep your data?" }), _jsx("p", { className: "text-muted text-sm leading-relaxed", children: "Snap-syncing the chain takes several gigabytes. Pick a location with plenty of free space." }), _jsx("input", { className: "input font-mono", value: cfg.dataDir, onChange: (e) => update({ dataDir: e.target.value }) }), _jsxs("div", { className: "flex justify-between", children: [_jsx("button", { className: "btn-ghost", onClick: () => setStep("welcome"), children: "Back" }), _jsx("button", { className: "btn-primary", onClick: () => setStep("syncmode"), children: "Continue" })] })] })), step === "syncmode" && (_jsxs("div", { className: "card space-y-5", children: [_jsx("h2", { className: "display-sm", children: "How should we sync?" }), _jsx(SyncOption, { k: "snap", title: "Snap (recommended)", desc: "Fastest first sync. Downloads recent state and verifies headers.", cfg: cfg, update: update }), _jsx(SyncOption, { k: "full", title: "Full", desc: "Verifies every block from genesis. Slower but most rigorous.", cfg: cfg, update: update }), _jsxs("div", { className: "flex justify-between pt-2", children: [_jsx("button", { className: "btn-ghost", onClick: () => setStep("datadir"), children: "Back" }), _jsx("button", { className: "btn-primary", onClick: () => setStep("networking"), children: "Continue" })] })] })), step === "networking" && (_jsxs("div", { className: "card space-y-5", children: [_jsx("h2", { className: "display-sm", children: "Help the network" }), _jsx("p", { className: "text-muted leading-relaxed", children: "Allow other peers on the Parallax network to dial your node. When enabled, your client opens a UPnP/PMP port mapping on your router so other peers can connect to you. This makes the network healthier and gives you faster block propagation." }), _jsx("p", { className: "text-muted text-sm leading-relaxed", children: "Your IP becomes discoverable by other peers when this is on. If you're behind a strict firewall or prefer to stay outbound-only, you can leave this off and still sync." }), _jsxs("div", { className: "flex items-center justify-between rounded border border-border bg-bg-elev-2 px-4 py-3", children: [_jsxs("div", { children: [_jsx("div", { className: "text-sm text-fg", children: "Allow inbound connections" }), _jsx("div", { className: "text-xs text-muted", children: "Recommended" })] }), _jsx(Toggle, { checked: !cfg.blockInbound, onChange: (v) => update({ blockInbound: !v }) })] }), _jsxs("div", { className: "flex justify-between pt-2", children: [_jsx("button", { className: "btn-ghost", onClick: () => setStep("syncmode"), children: "Back" }), _jsx("button", { className: "btn-primary", onClick: () => setStep("rpc"), children: "Continue" })] })] })), step === "rpc" && (_jsxs("div", { className: "card space-y-5", children: [_jsx("div", { className: "eyebrow", children: "Almost there" }), _jsx("h2", { className: "display-sm", children: "Local wallet access" }), _jsxs("p", { className: "text-muted leading-relaxed", children: ["Your node will expose a JSON-RPC endpoint on", " ", _jsxs("span", { className: "font-mono text-fg", children: ["http://127.0.0.1:", cfg.httpRpcPort] }), " ", "so MetaMask and other EVM-compatible wallets can connect to it. The endpoint is bound to the loopback interface only \u2014 never reachable from the public internet."] }), _jsx("p", { className: "text-muted text-sm leading-relaxed", children: "You can disable this later in Settings if you don't want any local app to connect." }), _jsxs("div", { className: "flex justify-between pt-2", children: [_jsx("button", { className: "btn-ghost", onClick: () => setStep("networking"), children: "Back" }), _jsx("button", { className: "btn-primary", onClick: finish, children: "Start node" })] })] })), step === "starting" && (_jsxs("div", { className: "card text-center py-16", children: [_jsx(motion.div, { className: "mx-auto h-12 w-12 rounded-full border-2 border-border-strong border-t-gold mb-6", animate: { rotate: 360 }, transition: { duration: 1.2, repeat: Infinity, ease: "linear" } }), _jsx("div", { className: "display-sm", children: "Starting your node\u2026" }), _jsx("p", { className: "text-muted text-sm mt-3", children: "This can take a minute on first launch." })] }))] }, step) })] })] }));
}
function SyncOption({ k, title, desc, cfg, update, }) {
    const selected = cfg.syncMode === k;
    return (_jsxs("button", { type: "button", onClick: () => update({ syncMode: k }), className: `text-left rounded border p-4 w-full transition-all duration-300 ${selected
            ? "border-gold bg-gold-muted shadow-gold-glow"
            : "border-border hover:bg-bg-elev hover:border-fg/20"}`, children: [_jsx("div", { className: "font-medium", children: title }), _jsx("div", { className: "text-sm text-muted", children: desc })] }));
}
