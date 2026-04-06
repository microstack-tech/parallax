import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { formatHashrate, formatPrlx, shortAddress } from "../lib/format";
export default function Mining() {
    const [accounts, setAccounts] = useState([]);
    const [coinbase, setCoinbase] = useState("");
    const [threads, setThreads] = useState(navigator.hardwareConcurrency || 4);
    const [stats, setStats] = useState(null);
    const [err, setErr] = useState(null);
    useEffect(() => {
        api.listAccounts().then((a) => {
            setAccounts(a);
            if (a.length && !coinbase)
                setCoinbase(a[0].address);
        });
    }, []);
    useEffect(() => {
        let alive = true;
        const tick = () => {
            api
                .miningStats()
                .then((s) => alive && setStats(s))
                .catch(() => { });
        };
        tick();
        const id = setInterval(tick, 2000);
        return () => {
            alive = false;
            clearInterval(id);
        };
    }, []);
    const start = async () => {
        setErr(null);
        try {
            await api.startMining(coinbase, threads);
        }
        catch (e) {
            setErr(e?.message || String(e));
        }
    };
    const stop = async () => {
        setErr(null);
        try {
            await api.stopMining();
        }
        catch (e) {
            setErr(e?.message || String(e));
        }
    };
    const onThreadsChange = async (n) => {
        setThreads(n);
        if (stats?.mining) {
            try {
                await api.setMinerThreads(n);
            }
            catch (e) {
                setErr(e?.message || String(e));
            }
        }
    };
    const maxThreads = navigator.hardwareConcurrency || 16;
    return (_jsxs("div", { className: "space-y-10 max-w-4xl mx-auto", children: [_jsxs("header", { children: [_jsx("div", { className: "eyebrow mb-3", children: "Mining" }), _jsx("h1", { className: "display", children: "Earn PRLX with your CPU." }), _jsx("p", { className: "text-muted mt-3 max-w-2xl", children: "XHash proof-of-work. Block reward halves every 210,000 blocks. Coinbase rewards are spendable after 100 confirmations." })] }), err && _jsx("div", { className: "card border-danger/40 bg-danger/10 text-danger", children: err }), _jsxs("section", { className: "card space-y-5", children: [_jsxs("div", { children: [_jsx("label", { className: "label", children: "Coinbase (reward recipient)" }), _jsx("select", { className: "input", value: coinbase, onChange: (e) => setCoinbase(e.target.value), children: accounts.map((a) => (_jsx("option", { value: a.address, children: a.label || a.address }, a.address))) })] }), _jsxs("div", { children: [_jsxs("label", { className: "label", children: ["CPU threads (", threads, " of ", maxThreads, ")"] }), _jsx("input", { type: "range", min: 1, max: maxThreads, value: threads, onChange: (e) => onThreadsChange(parseInt(e.target.value, 10)), className: "w-full accent-primary" })] }), _jsx("div", { className: "flex gap-3", children: stats?.mining ? (_jsx("button", { className: "btn-danger", onClick: stop, children: "Stop mining" })) : (_jsx("button", { className: "btn-primary", onClick: start, disabled: !coinbase, children: "Start mining" })) })] }), stats && (_jsxs("section", { className: "card", children: [_jsx("div", { className: "card-title mb-4", children: "Live stats" }), _jsxs("div", { className: "grid grid-cols-2 md:grid-cols-4 gap-6 mb-6", children: [_jsx(Stat, { label: "Status", value: stats.mining ? "Mining" : "Idle" }), _jsx(Stat, { label: "Hashrate", value: formatHashrate(stats.hashrateHps) }), _jsx(Stat, { label: "Difficulty", value: Number(stats.difficulty).toLocaleString() }), _jsx(Stat, { label: "Blocks found", value: stats.blocksFound.toString() })] }), _jsxs("div", { className: "grid grid-cols-1 md:grid-cols-3 gap-6", children: [_jsx(Stat, { label: "Mature reward", value: `${formatPrlx(stats.matureRewardWei, 4)} PRLX` }), _jsx(Stat, { label: "Immature reward", value: `${formatPrlx(stats.immatureRewardWei, 4)} PRLX` }), _jsx(Stat, { label: "Current block reward", value: `${formatPrlx(stats.currentBlockReward, 2)} PRLX` })] }), stats.coinbase && stats.coinbase !== "0x0000000000000000000000000000000000000000" && (_jsxs("div", { className: "text-xs text-muted mt-4", children: ["Rewards go to ", _jsx("span", { className: "font-mono", children: shortAddress(stats.coinbase, 10, 8) }), ". Coinbase outputs are spendable after 100 confirmations."] }))] }))] }));
}
function Stat({ label, value }) {
    return (_jsxs("div", { children: [_jsx("div", { className: "stat-label mb-2", children: label }), _jsx("div", { className: "stat-value", children: value })] }));
}
