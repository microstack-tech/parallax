import { useEffect, useState } from "react";
import { api, NodeStatus, openExternal } from "../lib/api";
import SectionHeading from "../components/SectionHeading";
import { PageStagger, StaggerItem } from "../components/PageStagger";

export default function Connect() {
  const [status, setStatus] = useState<NodeStatus | null>(null);
  const [helperURL, setHelperURL] = useState<string>("");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let alive = true;
    const refresh = async () => {
      try {
        const s = await api.nodeStatus();
        if (alive) setStatus(s);
      } catch {
        /* swallow */
      }
    };
    refresh();
    const id = setInterval(refresh, 4000);
    api.metaMaskHelperURL().then((u) => alive && setHelperURL(u)).catch(() => {});
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  const copyEndpoint = async () => {
    if (!status?.rpcEndpoint) return;
    await navigator.clipboard.writeText(status.rpcEndpoint);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  const enabled = !!status?.running && !!status?.rpcEndpoint;

  return (
    <PageStagger className="space-y-12 max-w-3xl mx-auto">
      <StaggerItem>
        <SectionHeading
          eyebrow="Connect"
          title="Use this node from MetaMask."
          subtitle="Your node exposes a JSON-RPC endpoint on the loopback interface. Add it as a custom network in any EVM-compatible wallet to sign transactions against your own node — no third-party RPC required."
        />
      </StaggerItem>

      {!enabled && (
        <StaggerItem>
          <div className="card border-gold/40 bg-gold-muted text-fg">
            <div className="eyebrow mb-2">Not available</div>
            <p className="text-sm text-fg/80 leading-relaxed">
              {status?.running
                ? "HTTP-RPC is currently disabled. Enable it from Settings to expose a local endpoint."
                : "Start the node from the Client page, then come back here to view your connection details."}
            </p>
          </div>
        </StaggerItem>
      )}

      {enabled && (
        <StaggerItem>
          <section className="card-featured">
            <div className="eyebrow mb-3">One click</div>
            <h2 className="display-sm mb-3">Add Parallax to MetaMask.</h2>
            <p className="text-muted leading-relaxed mb-6 max-w-lg">
              Opens a small page in your browser that prompts MetaMask (or any
              EIP-3085 compatible wallet) to add the Parallax network with your
              local RPC pre-filled. You only need to approve the prompt — no
              typing required.
            </p>
            <button
              className="btn-primary"
              disabled={!helperURL}
              onClick={() => helperURL && openExternal(helperURL)}
            >
              Add Parallax to MetaMask
            </button>
            <p className="text-xs text-muted mt-5 leading-relaxed">
              Don't use MetaMask? The manual values are below — they work with
              any EVM-compatible wallet.
            </p>
          </section>
        </StaggerItem>
      )}

      {enabled && (
        <StaggerItem>
          <section className="card">
            <div className="eyebrow mb-6">Manual setup</div>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-x-12 gap-y-6">
              <Field label="RPC URL">
                <div className="flex items-center gap-3">
                  <code className="font-mono text-sm text-fg">
                    {status!.rpcEndpoint}
                  </code>
                  <button onClick={copyEndpoint} className="link-arrow">
                    {copied ? "Copied" : "Copy"}
                  </button>
                </div>
              </Field>
              <Field label="Chain ID">
                <code className="font-mono text-sm text-fg">{status!.chainId}</code>
              </Field>
              <Field label="Currency symbol">
                <code className="font-mono text-sm text-fg">LAX</code>
              </Field>
              <Field label="Network name">
                <code className="font-mono text-sm text-fg">Parallax</code>
              </Field>
            </div>

            <p className="text-xs text-muted mt-7 leading-relaxed">
              The endpoint is bound to{" "}
              <span className="font-mono">127.0.0.1</span> only — it is not
              reachable from the public internet. You can disable it in
              Settings if you don't want any local app to connect.
            </p>
          </section>
        </StaggerItem>
      )}
    </PageStagger>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="eyebrow mb-1.5">{label}</div>
      <div>{children}</div>
    </div>
  );
}
