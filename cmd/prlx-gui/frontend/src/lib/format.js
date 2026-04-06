// Lightweight formatters used by the dashboard.
const WEI_PER_LAX = 10n ** 18n;
/** Format a wei string as LAX with up to `digits` fractional digits. */
export function formatLax(weiStr, digits = 4) {
    let wei;
    try {
        wei = BigInt(weiStr || "0");
    }
    catch {
        return "0";
    }
    const negative = wei < 0n;
    if (negative)
        wei = -wei;
    const whole = wei / WEI_PER_LAX;
    const frac = wei % WEI_PER_LAX;
    let fracStr = frac.toString().padStart(18, "0").slice(0, digits);
    fracStr = fracStr.replace(/0+$/, "");
    const out = fracStr ? `${whole}.${fracStr}` : whole.toString();
    return negative ? `-${out}` : out;
}
export function shortHex(s, head = 6, tail = 4) {
    if (!s || s.length < head + tail + 2)
        return s;
    return `${s.slice(0, head)}…${s.slice(-tail)}`;
}
/** Render a unix timestamp as a relative "Xs ago" / "Xm ago" / etc. */
export function timeAgo(unixSeconds) {
    const diff = Math.max(0, Math.floor(Date.now() / 1000 - unixSeconds));
    if (diff < 60)
        return `${diff}s ago`;
    if (diff < 3600)
        return `${Math.floor(diff / 60)}m ago`;
    if (diff < 86400)
        return `${Math.floor(diff / 3600)}h ago`;
    return `${Math.floor(diff / 86400)}d ago`;
}
export function formatBytes(n) {
    if (!n || n < 0)
        return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    let i = 0;
    let v = n;
    while (v >= 1024 && i < units.length - 1) {
        v /= 1024;
        i++;
    }
    return `${v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2)} ${units[i]}`;
}
export function formatDuration(seconds) {
    if (!seconds || seconds < 0)
        return "0s";
    const d = Math.floor(seconds / 86400);
    const h = Math.floor((seconds % 86400) / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    const s = Math.floor(seconds % 60);
    // Coarsest unit + one tier of detail; never seconds once minutes are
    // present. This keeps the dashboard's diff-based render skip from
    // firing on every 2s poll.
    if (d)
        return `${d}d ${h}h`;
    if (h)
        return `${h}h ${m}m`;
    if (m)
        return `${m}m`;
    return `${s}s`;
}
