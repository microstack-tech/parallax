import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { useEffect, useState } from "react";
import { api, openExternal } from "../lib/api";
import SectionHeading from "../components/SectionHeading";
import { PageStagger, StaggerItem } from "../components/PageStagger";
export default function Connect() {
    const [status, setStatus] = useState(null);
    const [helperURL, setHelperURL] = useState("");
    const [copied, setCopied] = useState(false);
    useEffect(() => {
        let alive = true;
        const refresh = async () => {
            try {
                const s = await api.nodeStatus();
                if (alive)
                    setStatus(s);
            }
            catch {
                /* swallow */
            }
        };
        refresh();
        const id = setInterval(refresh, 4000);
        api.metaMaskHelperURL().then((u) => alive && setHelperURL(u)).catch(() => { });
        return () => {
            alive = false;
            clearInterval(id);
        };
    }, []);
    const copyEndpoint = async () => {
        if (!status?.rpcEndpoint)
            return;
        await navigator.clipboard.writeText(status.rpcEndpoint);
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
    };
    const enabled = !!status?.running && !!status?.rpcEndpoint;
    return (_jsxs(PageStagger, { className: "space-y-12 max-w-3xl mx-auto", children: [_jsx(StaggerItem, { children: _jsx(SectionHeading, { eyebrow: "Connect", title: "Use this node from MetaMask.", subtitle: "Your node exposes a JSON-RPC endpoint on the loopback interface. Add it as a custom network in any EVM-compatible wallet to sign transactions against your own node \u2014 no third-party RPC required." }) }), !enabled && (_jsx(StaggerItem, { children: _jsxs("div", { className: "card border-gold/40 bg-gold-muted text-fg", children: [_jsx("div", { className: "eyebrow mb-2", children: "Not available" }), _jsx("p", { className: "text-sm text-fg/80 leading-relaxed", children: status?.running
                                ? "HTTP-RPC is currently disabled. Enable it from Settings to expose a local endpoint."
                                : "Start the node from the Client page, then come back here to view your connection details." })] }) })), enabled && (_jsx(StaggerItem, { children: _jsxs("section", { className: "card-featured", children: [_jsx("div", { className: "eyebrow mb-3", children: "One click" }), _jsx("h2", { className: "display-sm mb-3", children: "Add Parallax to MetaMask." }), _jsx("p", { className: "text-muted leading-relaxed mb-6 max-w-lg", children: "Opens a small page in your browser that prompts MetaMask (or any EIP-3085 compatible wallet) to add the Parallax network with your local RPC pre-filled. You only need to approve the prompt \u2014 no typing required." }), _jsx("button", { className: "btn-primary", disabled: !helperURL, onClick: () => helperURL && openExternal(helperURL), children: "Add Parallax to MetaMask" }), _jsx("p", { className: "text-xs text-muted mt-5 leading-relaxed", children: "Don't use MetaMask? The manual values are below \u2014 they work with any EVM-compatible wallet." })] }) })), enabled && (_jsx(StaggerItem, { children: _jsxs("section", { className: "card", children: [_jsx("div", { className: "eyebrow mb-6", children: "Manual setup" }), _jsxs("div", { className: "grid grid-cols-1 md:grid-cols-2 gap-x-12 gap-y-6", children: [_jsx(Field, { label: "RPC URL", children: _jsxs("div", { className: "flex items-center gap-3", children: [_jsx("code", { className: "font-mono text-sm text-fg", children: status.rpcEndpoint }), _jsx("button", { onClick: copyEndpoint, className: "link-arrow", children: copied ? "Copied" : "Copy" })] }) }), _jsx(Field, { label: "Chain ID", children: _jsx("code", { className: "font-mono text-sm text-fg", children: status.chainId }) }), _jsx(Field, { label: "Currency symbol", children: _jsx("code", { className: "font-mono text-sm text-fg", children: "LAX" }) }), _jsx(Field, { label: "Network name", children: _jsx("code", { className: "font-mono text-sm text-fg", children: "Parallax" }) })] }), _jsxs("p", { className: "text-xs text-muted mt-7 leading-relaxed", children: ["The endpoint is bound to", " ", _jsx("span", { className: "font-mono", children: "127.0.0.1" }), " only \u2014 it is not reachable from the public internet. You can disable it in Settings if you don't want any local app to connect."] })] }) }))] }));
}
function Field({ label, children }) {
    return (_jsxs("div", { children: [_jsx("div", { className: "eyebrow mb-1.5", children: label }), _jsx("div", { children: children })] }));
}
