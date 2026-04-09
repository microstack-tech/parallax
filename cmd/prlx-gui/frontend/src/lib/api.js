// Typed wrapper around the Wails-bound App methods exposed by the Go backend.
//
// We deliberately do not import from `wailsjs/go/main/App` (the file Wails
// generates) so that the frontend can be type-checked before the Go bindings
// have been generated. The runtime call still resolves through the same
// window.go.main.App injection point.
/** Open a URL in the user's default browser via the Wails runtime. */
export function openExternal(url) {
    if (window.runtime?.BrowserOpenURL) {
        window.runtime.BrowserOpenURL(url);
    }
    else {
        window.open(url, "_blank", "noreferrer");
    }
}
const call = (method, ...args) => {
    const fn = window?.go?.main?.App?.[method];
    if (typeof fn !== "function") {
        return Promise.reject(new Error(`backend method ${method} unavailable`));
    }
    return fn(...args);
};
export const api = {
    bootstrapNeeded: () => call("BootstrapNeeded"),
    saveBootstrap: (cfg) => call("SaveBootstrap", cfg),
    getConfig: () => call("GetConfig"),
    updateConfig: (cfg) => call("UpdateConfig", cfg),
    startNode: () => call("StartNode"),
    stopNode: () => call("StopNode"),
    nodeStatus: () => call("NodeStatus"),
    peers: () => call("Peers"),
    recentBlocks: (n) => call("RecentBlocks", n),
    recentTransactions: (n) => call("RecentTransactions", n),
    getLogTail: (n) => call("GetLogTail", n),
    setLogVerbosity: (n) => call("SetLogVerbosity", n),
    metaMaskHelperURL: () => call("MetaMaskHelperURL"),
    // Mining
    startMining: (mode) => call("StartMining", mode),
    stopMining: () => call("StopMining"),
    minerStatus: () => call("MinerStatus"),
    detectGPUs: () => call("DetectGPUs"),
    defaultPools: () => call("DefaultPools"),
    hashwarpInstalled: () => call("HashwarpInstalled"),
    installHashwarp: (gpuType) => call("InstallHashwarp", gpuType),
    version: () => call("Version"),
    clientVersion: () => call("ClientVersion"),
};
