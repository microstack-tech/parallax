// Typed wrapper around the Wails-bound App methods exposed by the Go backend.
//
// We deliberately do not import from `wailsjs/go/main/App` (the file Wails
// generates) so that the frontend can be type-checked before the Go bindings
// have been generated. The runtime call still resolves through the same
// window.go.main.App injection point.

declare global {
  interface Window {
    go: any;
    runtime: {
      EventsOn: (event: string, cb: (data: any) => void) => () => void;
      EventsOff: (event: string) => void;
      BrowserOpenURL: (url: string) => void;
    };
  }
}

/** Open a URL in the user's default browser via the Wails runtime. */
export function openExternal(url: string) {
  if (window.runtime?.BrowserOpenURL) {
    window.runtime.BrowserOpenURL(url);
  } else {
    window.open(url, "_blank", "noreferrer");
  }
}

const call = <T = any>(method: string, ...args: any[]): Promise<T> => {
  const fn = window?.go?.main?.App?.[method];
  if (typeof fn !== "function") {
    return Promise.reject(new Error(`backend method ${method} unavailable`));
  }
  return fn(...args);
};

export type GUIConfig = {
  bootstrapped: boolean;
  dataDir: string;
  syncMode: "snap" | "full";
  httpRpcEnabled: boolean;
  httpRpcPort: number;
  blockInbound: boolean;
  maxPeers: number;
  theme: "system" | "light" | "dark";
  autoStartNode: boolean;
  databaseCacheMB: number;
  trieCleanCacheMB: number;
  trieDirtyCacheMB: number;
  snapshotCacheMB: number;
};

export type NodeStatus = {
  running: boolean;
  syncing: boolean;
  currentBlock: number;
  highestBlock: number;
  startingBlock: number;
  peers: number;
  chainId: number;
  networkId: number;
  dataDir: string;
  uptimeSeconds: number;
  diskUsedBytes: number;
  memUsedBytes: number;
  clientVersion: string;
  rpcEndpoint: string;
};

export type LogLine = {
  ts: number;
  level: string;
  msg: string;
};

export type PeerView = {
  id: string;
  fullId: string;
  enode: string;
  name: string;
  remoteAddr: string;
  localAddr: string;
  inbound: boolean;
  trusted: boolean;
  static: boolean;
  caps: string[];
  protocols?: Record<string, any>;
};

export type BlockView = {
  number: number;
  hash: string;
  timestamp: number;
  txCount: number;
  gasUsed: number;
  gasLimit: number;
  sizeBytes: number;
  coinbase: string;
  rewardWei: string;
  difficulty: string;
};

export type TxView = {
  hash: string;
  block: number;
  timestamp: number;
  from: string;
  to: string;
  valueWei: string;
  gasUsed: number;
  kind: "transfer" | "contract" | "call";
};

export const api = {
  bootstrapNeeded: () => call<boolean>("BootstrapNeeded"),
  saveBootstrap: (cfg: GUIConfig) => call<void>("SaveBootstrap", cfg),
  getConfig: () => call<GUIConfig>("GetConfig"),
  updateConfig: (cfg: GUIConfig) => call<void>("UpdateConfig", cfg),

  startNode: () => call<void>("StartNode"),
  stopNode: () => call<void>("StopNode"),
  nodeStatus: () => call<NodeStatus>("NodeStatus"),
  peers: () => call<PeerView[]>("Peers"),
  recentBlocks: (n: number) => call<BlockView[]>("RecentBlocks", n),
  recentTransactions: (n: number) => call<TxView[]>("RecentTransactions", n),

  getLogTail: (n: number) => call<LogLine[]>("GetLogTail", n),
  setLogVerbosity: (n: number) => call<void>("SetLogVerbosity", n),

  metaMaskHelperURL: () => call<string>("MetaMaskHelperURL"),

  version: () => call<string>("Version"),
  clientVersion: () => call<string>("ClientVersion"),
};
