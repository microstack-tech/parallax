import { useEffect, useState } from "react";
import {
  api,
  GUIConfig,
  NodeStatus,
  MinerStatus,
  MiningMode,
  DeviceInfo,
  PoolInfo,
} from "../lib/api";
import { formatHashrate, formatDuration, formatDifficulty } from "../lib/format";
import SectionHeading from "../components/SectionHeading";
import AnimatedNumber from "../components/AnimatedNumber";
import { PageStagger, StaggerItem } from "../components/PageStagger";

export default function Mining() {
  const [cfg, setCfg] = useState<GUIConfig | null>(null);
  const [status, setStatus] = useState<MinerStatus | null>(null);
  const [nodeStatus, setNodeStatus] = useState<NodeStatus | null>(null);
  const [gpus, setGpus] = useState<DeviceInfo[]>([]);
  const [pools, setPools] = useState<PoolInfo[]>([]);
  const [selectedMode, setSelectedMode] = useState<"pool" | "sologpu" | "solo">("pool");
  const [err, setErr] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [hashwarpFound, setHashwarpFound] = useState<boolean | null>(null); // null = loading

  // Form state (kept in sync with config)
  const [wallet, setWallet] = useState("");
  const [worker, setWorker] = useState("");
  const [poolUrl, setPoolUrl] = useState("");
  const [threads, setThreads] = useState(
    Math.max(1, Math.floor(navigator.hardwareConcurrency / 2)) || 2,
  );
  const [selectedDevices, setSelectedDevices] = useState<number[]>([]);
  const [showCustomPool, setShowCustomPool] = useState(false);
  const [editingPoolUrl, setEditingPoolUrl] = useState<string | null>(null); // url of pool being edited, null = adding new
  const [customName, setCustomName] = useState("");
  const [customUrl, setCustomUrl] = useState("");
  const [customRegion, setCustomRegion] = useState("");

  // Load config, pools, GPUs, and check hashwarp on mount
  useEffect(() => {
    api.hashwarpInstalled().then(setHashwarpFound).catch(() => setHashwarpFound(false));
    api.getConfig().then((c) => {
      setCfg(c);
      if (c.miningWallet) setWallet(c.miningWallet);
      if (c.miningWorker) setWorker(c.miningWorker);
      if (c.miningPool) setPoolUrl(c.miningPool);
      if (c.miningThreads > 0) setThreads(c.miningThreads);
      if (c.miningDevices?.length) setSelectedDevices(c.miningDevices);
    });
    api.defaultPools().then((dp) => {
      api.getConfig().then((c) => {
        const custom = c.customPools || [];
        setPools([...dp, ...custom]);
        if (!c.miningPool && dp.length > 0) {
          setPoolUrl(dp[0].url);
        }
      });
    });
    api.detectGPUs().then(setGpus).catch(() => setGpus([]));
  }, []);

  // Poll miner + node status every 2s
  useEffect(() => {
    let alive = true;
    const refresh = async () => {
      const [ms, ns] = await Promise.all([
        api.minerStatus().catch(() => null),
        api.nodeStatus().catch(() => null),
      ]);
      if (!alive) return;
      if (ms) setStatus(ms);
      if (ns) setNodeStatus(ns);
    };
    refresh();
    const id = setInterval(refresh, 2000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  const isRunning = status?.running ?? false;

  const persistConfig = (patch: Partial<GUIConfig>) => {
    if (!cfg) return;
    const updated = { ...cfg, ...patch };
    setCfg(updated);
    api.updateConfig(updated).catch(() => { });
  };

  const start = async () => {
    setErr(null);
    setStarting(true);
    try {
      // Persist settings before starting
      persistConfig({
        miningWallet: wallet,
        miningWorker: worker,
        miningPool: poolUrl,
        miningThreads: threads,
        miningDevices: selectedDevices,
      });
      await api.startMining(selectedMode as MiningMode);
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setStarting(false);
    }
  };

  const stop = async () => {
    setErr(null);
    setStopping(true);
    try {
      await api.stopMining();
    } catch (e: any) {
      setErr(e?.message || String(e));
    } finally {
      setStopping(false);
    }
  };

  const resetPoolForm = () => {
    setShowCustomPool(false);
    setEditingPoolUrl(null);
    setCustomName("");
    setCustomUrl("");
    setCustomRegion("");
  };

  const openAddPool = () => {
    setEditingPoolUrl(null);
    setCustomName("");
    setCustomUrl("");
    setCustomRegion("");
    setShowCustomPool(true);
  };

  const openEditPool = (p: PoolInfo) => {
    setEditingPoolUrl(p.url);
    setCustomName(p.name);
    setCustomUrl(p.url);
    setCustomRegion(p.region);
    setShowCustomPool(true);
  };

  const savePool = () => {
    if (!customUrl || !customName) return;
    const newPool: PoolInfo = {
      name: customName,
      url: customUrl,
      region: customRegion,
      builtin: false,
    };
    let updated: PoolInfo[];
    if (editingPoolUrl) {
      // Replace existing
      updated = pools.map((p) =>
        !p.builtin && p.url === editingPoolUrl ? newPool : p,
      );
    } else {
      // Add new
      updated = [...pools, newPool];
    }
    setPools(updated);
    setPoolUrl(customUrl);
    resetPoolForm();
    persistConfig({
      customPools: updated.filter((p) => !p.builtin),
      miningPool: customUrl,
    });
  };

  const deletePool = (url: string) => {
    const updated = pools.filter((p) => p.url !== url);
    setPools(updated);
    if (poolUrl === url && updated.length > 0) {
      setPoolUrl(updated[0].url);
    }
    resetPoolForm();
    persistConfig({
      customPools: updated.filter((p) => !p.builtin),
      miningPool: poolUrl === url ? updated[0]?.url || "" : poolUrl,
    });
  };

  const toggleDevice = (idx: number) => {
    setSelectedDevices((prev) =>
      prev.includes(idx) ? prev.filter((d) => d !== idx) : [...prev, idx],
    );
  };

  const maxThreads = navigator.hardwareConcurrency || 16;
  const nodeSynced = nodeStatus?.running && !nodeStatus?.syncing;

  return (
    <PageStagger className="space-y-14 max-w-5xl mx-auto">
      <StaggerItem>
        <SectionHeading
          eyebrow="Mining"
          title="Earn LAX with your hardware."
          subtitle="Mine to a pool with your GPU, or solo mine with your CPU through the local node."
        />
        <p className="text-muted text-sm mt-5 max-w-2xl">
          By mining, you help decentralize the Parallax network and secure every
          transaction on the blockchain. The more miners participate, the
          stronger and more censorship-resistant the network becomes.
        </p>
      </StaggerItem>

      {err && (
        <StaggerItem>
          <div className="card border-danger/40 bg-danger/10 text-danger">
            {err}
          </div>
        </StaggerItem>
      )}

      {status?.error && !err && (
        <StaggerItem>
          <div className="card border-danger/40 bg-danger/10 text-danger">
            {status.error}
          </div>
        </StaggerItem>
      )}

      {/* ── Mode Selector ── */}
      {!isRunning && (
        <StaggerItem>
          <div className="flex gap-2 p-1 rounded-lg bg-bg-elev border border-border w-fit">
            {([
              ["pool", "Pool (GPU)"],
              ["sologpu", "Solo (GPU)"],
              ["solo", "Solo (CPU)"],
            ] as const).map(([mode, label]) => (
              <button
                key={mode}
                className={`px-5 py-2.5 rounded-md text-sm font-medium transition-all ${selectedMode === mode
                    ? "bg-gold text-gold-fg shadow-sm"
                    : "text-muted hover:text-fg"
                  }`}
                onClick={() => setSelectedMode(mode)}
              >
                {label}
              </button>
            ))}
          </div>
          <p className="text-xs text-muted mt-3">
            {selectedMode === "pool" &&
              "Mine through a pool — rewards are split among all miners. No node required."}
            {selectedMode === "sologpu" &&
              "Mine solo with your GPU — you keep the full block reward. Requires a running, synced node."}
            {selectedMode === "solo" &&
              "Mine solo with your CPU — lower hashrate but no GPU needed. Requires a running, synced node."}
          </p>
        </StaggerItem>
      )}

      {/* ── Hashwarp Setup Guide (GPU modes, binary not found) ── */}
      {!isRunning && (selectedMode === "pool" || selectedMode === "sologpu") && hashwarpFound === false && (
        <StaggerItem>
          <HashwarpSetupGuide onRetry={() => {
            setHashwarpFound(null);
            api.hashwarpInstalled().then((found) => {
              setHashwarpFound(found);
              if (found) api.detectGPUs().then(setGpus).catch(() => setGpus([]));
            }).catch(() => setHashwarpFound(false));
          }} />
        </StaggerItem>
      )}

      {/* ── Configuration ── */}
      {!isRunning && !((selectedMode === "pool" || selectedMode === "sologpu") && hashwarpFound === false) && (
        <StaggerItem>
          <section className="card space-y-6">
            <div className="eyebrow mb-2">Configuration</div>

            {/* Wallet (both modes) */}
            <div>
              <label className="label">Wallet address</label>
              <input
                type="text"
                className="input font-mono"
                placeholder="0x..."
                value={wallet}
                onChange={(e) => setWallet(e.target.value)}
              />
            </div>

            {/* Pool-only: worker name & pool selector */}
            {selectedMode === "pool" && (
              <>
                <div>
                  <label className="label">Worker name</label>
                  <input
                    type="text"
                    className="input"
                    placeholder="my-rig"
                    value={worker}
                    onChange={(e) => setWorker(e.target.value)}
                  />
                </div>

                <div>
                  <label className="label">Mining pool</label>
                  <select
                    className="input"
                    value={poolUrl}
                    onChange={(e) => setPoolUrl(e.target.value)}
                  >
                    {pools.map((p) => (
                      <option key={p.url} value={p.url}>
                        {p.name}
                        {p.region ? ` (${p.region})` : ""}
                      </option>
                    ))}
                  </select>
                  <div className="flex items-center gap-3 mt-2">
                    <button
                      className="text-xs text-gold hover:text-gold/80 transition-colors"
                      onClick={showCustomPool ? resetPoolForm : openAddPool}
                    >
                      {showCustomPool ? "Cancel" : "+ Add custom pool"}
                    </button>
                    {(() => {
                      const selected = pools.find((p) => p.url === poolUrl);
                      if (!selected || selected.builtin) return null;
                      return (
                        <>
                          <button
                            className="text-xs text-muted hover:text-fg transition-colors"
                            onClick={() => openEditPool(selected)}
                          >
                            Edit
                          </button>
                          <button
                            className="text-xs text-danger/70 hover:text-danger transition-colors"
                            onClick={() => deletePool(selected.url)}
                          >
                            Remove
                          </button>
                        </>
                      );
                    })()}
                  </div>
                </div>

                {showCustomPool && (
                  <div className="rounded-lg border border-border bg-bg-elev p-4 space-y-3">
                    <div className="grid grid-cols-2 gap-3">
                      <div>
                        <label className="label">Pool name</label>
                        <input
                          type="text"
                          className="input"
                          placeholder="My Pool"
                          value={customName}
                          onChange={(e) => setCustomName(e.target.value)}
                        />
                      </div>
                      <div>
                        <label className="label">Region (optional)</label>
                        <input
                          type="text"
                          className="input"
                          placeholder="US"
                          value={customRegion}
                          onChange={(e) => setCustomRegion(e.target.value)}
                        />
                      </div>
                    </div>
                    <div>
                      <label className="label">Stratum URL</label>
                      <input
                        type="text"
                        className="input font-mono"
                        placeholder="stratum1+tcp://pool.example.com:4444"
                        value={customUrl}
                        onChange={(e) => setCustomUrl(e.target.value)}
                      />
                    </div>
                    <div className="flex gap-2">
                      <button
                        className="btn-primary"
                        onClick={savePool}
                        disabled={!customName || !customUrl}
                      >
                        {editingPoolUrl ? "Save changes" : "Add pool"}
                      </button>
                      <button className="btn-ghost" onClick={resetPoolForm}>
                        Cancel
                      </button>
                    </div>
                  </div>
                )}
              </>
            )}

            {/* GPU devices (pool & sologpu) */}
            {(selectedMode === "pool" || selectedMode === "sologpu") && (
              <>
                {gpus.length > 0 && (
                  <div>
                    <label className="label">GPU devices</label>
                    <div className="space-y-2 mt-1">
                      {gpus.map((g) => (
                        <label
                          key={g.index}
                          className="flex items-center gap-3 rounded-lg border border-border bg-bg-elev px-4 py-3 cursor-pointer hover:border-fg/20 transition-colors"
                        >
                          <input
                            type="checkbox"
                            checked={
                              selectedDevices.length === 0 ||
                              selectedDevices.includes(g.index)
                            }
                            onChange={() => toggleDevice(g.index)}
                            className="accent-gold"
                          />
                          <div className="flex-1 min-w-0">
                            <div className="text-sm font-medium text-fg truncate">
                              GPU {g.index}: {g.name}
                            </div>
                            <div className="text-xs text-muted">
                              {g.memory}
                              {g.cuda && " · CUDA"}
                              {g.openCl && " · OpenCL"}
                            </div>
                          </div>
                        </label>
                      ))}
                    </div>
                    {selectedDevices.length === 0 && (
                      <p className="text-xs text-muted mt-1">
                        All devices selected by default.
                      </p>
                    )}
                  </div>
                )}
                {gpus.length === 0 && (
                  <div className="rounded-lg border border-border bg-bg-elev px-4 py-3">
                    <p className="text-sm text-muted">
                      No GPUs detected. Make sure hashwarp is installed. You can
                      still use Solo (CPU) mode.
                    </p>
                  </div>
                )}
              </>
            )}

            {/* Node status (sologpu & solo) */}
            {(selectedMode === "sologpu" || selectedMode === "solo") && (
              <div className="rounded-lg border border-border bg-bg-elev px-4 py-3 flex items-center justify-between gap-3">
                <div className="flex items-center gap-3">
                  <span
                    className={nodeSynced ? "live-dot-success" : "live-dot-err"}
                  />
                  <span className="text-sm">
                    {!nodeStatus?.running
                      ? "Node is not running."
                      : nodeStatus.syncing
                        ? "Node is syncing. Solo mining requires a fully synced node."
                        : "Node is synced and ready for solo mining."}
                  </span>
                </div>
                {!nodeStatus?.running && (
                  <button
                    className="btn-primary text-sm shrink-0"
                    onClick={async () => {
                      try {
                        await api.startNode();
                      } catch (e: any) {
                        setErr(e?.message || String(e));
                      }
                    }}
                  >
                    Start node
                  </button>
                )}
              </div>
            )}

            {/* CPU threads (solo CPU only) */}
            {selectedMode === "solo" && (
              <div>
                <label className="label">
                  CPU threads ({threads} of {maxThreads})
                </label>
                <input
                  type="range"
                  min={1}
                  max={maxThreads}
                  value={threads}
                  onChange={(e) => setThreads(parseInt(e.target.value, 10))}
                  className="w-full accent-gold"
                />
              </div>
            )}

            {/* Start button */}
            <button
              className="btn-primary"
              onClick={start}
              disabled={
                starting ||
                !wallet ||
                (selectedMode === "pool" && !poolUrl) ||
                ((selectedMode === "sologpu" || selectedMode === "solo") && !nodeSynced)
              }
            >
              {starting
                ? "Starting..."
                : selectedMode === "pool"
                  ? "Start Pool Mining"
                  : selectedMode === "sologpu"
                    ? "Start Solo GPU Mining"
                    : "Start Solo CPU Mining"}
            </button>
          </section>
        </StaggerItem>
      )}

      {/* ── Live Stats ── */}
      {isRunning && status && (
        <StaggerItem>
          <section className="card-featured">
            <div className="flex items-center justify-between mb-7">
              <div className="eyebrow">Live Stats</div>
              <span className="pill-ok">
                <span className="live-dot-success" />
                {status.generatingDag ? "Generating DAG" : "Mining"}
              </span>
            </div>

            {/* DAG generation indicator */}
            {status.generatingDag && (
              <div className="mb-6">
                <div className="flex justify-between eyebrow mb-3">
                  <span>Generating DAG for epoch {status.epoch}</span>
                </div>
                <div className="progress-track">
                  <div className="progress-fill animate-pulse-soft w-full" />
                </div>
              </div>
            )}

            <div className="grid grid-cols-2 md:grid-cols-4 gap-8 mb-6">
              <Stat label="Hashrate">
                <AnimatedNumber
                  value={status.hashrate}
                  format={formatHashrate}
                  className="stat-value"
                />
              </Stat>

              {(status.mode === "pool" || status.mode === "sologpu") && (
                <Stat label="Shares">
                  <span className="stat-value">
                    {status.sharesAccepted.toLocaleString()}
                    {status.sharesRejected > 0 && (
                      <span className="text-danger">
                        /{status.sharesRejected}
                      </span>
                    )}
                    {status.sharesFailed > 0 && (
                      <span className="text-danger">
                        /{status.sharesFailed}F
                      </span>
                    )}
                  </span>
                </Stat>
              )}

              {status.mode === "solo" && (
                <Stat label="Difficulty">
                  <span className="stat-value">
                    {formatDifficulty(status.soloDifficulty || "0")}
                  </span>
                </Stat>
              )}

              <Stat label="Uptime">
                <span className="stat-value">
                  {formatDuration(status.uptimeSeconds)}
                </span>
              </Stat>

              {status.mode === "pool" && (
                <Stat label="Pool">
                  <span className="stat-value text-sm">
                    <span
                      className={
                        status.poolConnected
                          ? "live-dot-success"
                          : "live-dot-err"
                      }
                    />{" "}
                    {status.poolConnected ? "Connected" : "Disconnected"}
                  </span>
                </Stat>
              )}

              {status.mode === "sologpu" && (
                <Stat label="Node">
                  <span className="stat-value text-sm">
                    <span
                      className={
                        status.poolConnected
                          ? "live-dot-success"
                          : "live-dot-err"
                      }
                    />{" "}
                    {status.poolConnected ? "Connected" : "Disconnected"}
                  </span>
                </Stat>
              )}

              {status.mode === "solo" && (
                <Stat label="Blocks Found">
                  <span className="stat-value">
                    {status.soloBlocksFound.toLocaleString()}
                  </span>
                </Stat>
              )}
            </div>
          </section>
        </StaggerItem>
      )}

      {/* ── GPU Devices ── */}
      {isRunning &&
        (status?.mode === "pool" || status?.mode === "sologpu") &&
        status?.devices &&
        status.devices.length > 0 && (
          <StaggerItem>
            <section className="card space-y-4">
              <div className="eyebrow mb-2">GPU Devices</div>
              <div className="grid gap-3">
                {status.devices.map((d) => (
                  <div
                    key={d.index}
                    className="flex items-center justify-between gap-4 rounded-lg border border-border bg-bg-elev px-5 py-3"
                  >
                    <div className="min-w-0">
                      <div className="text-sm font-medium text-fg truncate">
                        #{d.index} {d.name}
                      </div>
                      <div className="text-xs text-muted mt-0.5">
                        {d.mode} · {d.accepted} accepted
                        {d.rejected > 0 && ` · ${d.rejected} rejected`}
                      </div>
                    </div>
                    <div className="flex items-center gap-6 text-sm shrink-0">
                      <span className="font-mono text-fg">
                        {formatHashrate(d.hashrate)}
                      </span>
                      {d.tempC > 0 && (
                        <span
                          className={
                            d.tempC > 80 ? "text-danger" : "text-muted"
                          }
                        >
                          {d.tempC}°C
                        </span>
                      )}
                      {d.fanPct > 0 && (
                        <span className="text-muted">{d.fanPct}%</span>
                      )}
                      {d.powerW > 0 && (
                        <span className="text-muted">
                          {d.powerW.toFixed(0)}W
                        </span>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </section>
          </StaggerItem>
        )}

      {/* ── Stop button ── */}
      {isRunning && (
        <StaggerItem>
          <button
            className="btn-danger"
            onClick={stop}
            disabled={stopping}
          >
            {stopping ? "Stopping..." : "Stop Mining"}
          </button>
        </StaggerItem>
      )}
    </PageStagger>
  );
}

function HashwarpSetupGuide({ onRetry }: { onRetry: () => void }) {
  const [installing, setInstalling] = useState(false);
  const [step, setStep] = useState("");
  const [installErr, setInstallErr] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const isWindows = navigator.userAgent.includes("Windows");

  useEffect(() => {
    const off = window.runtime.EventsOn(
      "hashwarp-install",
      (data: { step: string; detail: string }) => {
        setStep(
          data.step === "finding"
            ? "Looking up latest release..."
            : data.step === "downloading"
              ? `Downloading ${data.detail}...`
              : data.step === "extracting"
                ? "Extracting..."
                : data.step === "done"
                  ? "Installed!"
                  : data.step,
        );
        if (data.step === "done") setDone(true);
      },
    );
    return () => off();
  }, []);

  const install = async (gpuType: "cuda" | "opencl") => {
    setInstalling(true);
    setInstallErr(null);
    setStep("Starting...");
    setDone(false);
    try {
      await api.installHashwarp(gpuType);
      // Small delay so the user sees "Installed!" before transition
      setTimeout(() => onRetry(), 1000);
    } catch (e: any) {
      setInstallErr(e?.message || String(e));
      setInstalling(false);
    }
  };

  if (installing) {
    return (
      <section className="card space-y-6">
        <div className="eyebrow">Installing GPU Miner</div>
        <div className="flex items-center gap-4">
          {!done && !installErr && (
            <div className="w-5 h-5 border-2 border-gold border-t-transparent rounded-full animate-spin" />
          )}
          {done && (
            <div className="w-5 h-5 rounded-full bg-success flex items-center justify-center text-xs text-bg font-bold">
              ✓
            </div>
          )}
          <span className="text-fg text-sm">{step}</span>
        </div>
        {!done && !installErr && (
          <div className="progress-track">
            <div className="progress-fill animate-pulse-soft w-full" />
          </div>
        )}
        {installErr && (
          <div className="space-y-3">
            <div className="rounded-lg border border-danger/40 bg-danger/10 text-danger text-sm px-4 py-3">
              {installErr}
            </div>
            <button
              className="btn-ghost text-sm"
              onClick={() => setInstalling(false)}
            >
              Back
            </button>
          </div>
        )}
      </section>
    );
  }

  return (
    <section className="card space-y-6">
      <div>
        <div className="eyebrow mb-3">GPU Miner Setup</div>
        <p className="text-fg text-lg font-medium">
          Select your GPU brand to install the mining engine.
        </p>
        <p className="text-muted mt-2 text-sm">
          Parallax Desktop will automatically download and set up Hashwarp, the
          GPU mining software. Just pick your graphics card brand below.
        </p>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <button
          className="group rounded-lg border border-border bg-bg-elev p-5 text-left hover:border-gold/40 hover:shadow-[0_0_30px_rgb(247_147_26/0.08)] transition-all"
          onClick={() => install("cuda")}
        >
          <div className="text-fg font-medium text-base mb-1">NVIDIA</div>
          <div className="text-xs text-muted">
            GeForce GTX / RTX series
          </div>
          <div className="text-xs text-gold mt-3 opacity-70 group-hover:opacity-100 transition-opacity">
            Download CUDA version →
          </div>
        </button>
        <button
          className="group rounded-lg border border-border bg-bg-elev p-5 text-left hover:border-gold/40 hover:shadow-[0_0_30px_rgb(247_147_26/0.08)] transition-all"
          onClick={() => install("opencl")}
        >
          <div className="text-fg font-medium text-base mb-1">AMD</div>
          <div className="text-xs text-muted">
            Radeon RX series
          </div>
          <div className="text-xs text-gold mt-3 opacity-70 group-hover:opacity-100 transition-opacity">
            Download OpenCL version →
          </div>
        </button>
      </div>

      {!isWindows && (
        <p className="text-xs text-muted">
          Not sure which GPU you have? Run{" "}
          <code className="text-fg bg-bg-elev px-1.5 py-0.5 rounded text-xs">
            lspci | grep -i vga
          </code>{" "}
          in a terminal.
        </p>
      )}
      {isWindows && (
        <p className="text-xs text-muted">
          Not sure which GPU you have? Check Device Manager under "Display
          adapters".
        </p>
      )}
    </section>
  );
}

function Stat({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="stat-label mb-2">{label}</div>
      {children}
    </div>
  );
}
