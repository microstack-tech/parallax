import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { motion } from "motion/react";
import { api, LogLine } from "../lib/api";
import SectionHeading from "../components/SectionHeading";
import { PageStagger, StaggerItem } from "../components/PageStagger";

export default function Logs() {
  const [lines, setLines] = useState<LogLine[]>([]);
  const [filter, setFilter] = useState("");
  const [paused, setPaused] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    api.getLogTail(500).then(setLines);
    const off = window.runtime?.EventsOn?.("log", (line: LogLine) => {
      setLines((prev) => {
        const next = [...prev, line];
        return next.length > 2000 ? next.slice(next.length - 2000) : next;
      });
    });
    return () => {
      if (typeof off === "function") off();
    };
  }, []);

  useEffect(() => {
    if (paused) return;
    if (ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [lines, paused]);

  const visible = filter
    ? lines.filter((l) => l.msg.toLowerCase().includes(filter.toLowerCase()))
    : lines;

  return (
    <PageStagger className="space-y-10 max-w-6xl mx-auto h-full flex flex-col">
      <StaggerItem>
        <SectionHeading
          eyebrow="Logs"
          title="Live tail."
          subtitle="Streaming directly from the embedded node, level INFO and above."
          trailing={
            <div className="flex items-center gap-4">
              <span className="flex items-center gap-2 text-xs text-muted">
                <span
                  className={
                    paused ? "live-dot bg-muted" : "live-dot-success"
                  }
                />
                {paused ? "Paused" : "Live"}
              </span>
              <Link to="/settings" className="btn-ghost">
                ← Back
              </Link>
            </div>
          }
        />
      </StaggerItem>

      <StaggerItem className="flex-1 flex flex-col min-h-0">
        <div className="flex gap-2 mb-4">
          <input
            className="input flex-1"
            placeholder="Filter…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
          <button
            className="btn-ghost"
            onClick={() => setPaused((p) => !p)}
          >
            {paused ? "Resume" : "Pause"}
          </button>
          <button
            className="btn-ghost"
            onClick={() =>
              navigator.clipboard.writeText(visible.map((l) => l.msg).join("\n"))
            }
          >
            Copy
          </button>
        </div>

        <motion.div
          ref={ref}
          className="card flex-1 min-h-0 overflow-y-auto font-mono text-xs whitespace-pre-wrap leading-relaxed"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          transition={{ duration: 0.5 }}
        >
          {visible.length === 0 ? (
            <p className="text-muted not-italic">No log lines yet.</p>
          ) : (
            visible.map((l, i) => (
              <div key={i} className="grid grid-cols-[60px_1fr] gap-3 py-0.5">
                <span className="text-muted">{l.level.trim()}</span>
                <span className="text-fg/85">{l.msg}</span>
              </div>
            ))
          )}
        </motion.div>
      </StaggerItem>
    </PageStagger>
  );
}
