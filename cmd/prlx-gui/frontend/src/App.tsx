import { useEffect, useState } from "react";
import { Routes, Route, NavLink, Navigate, useNavigate, useLocation } from "react-router-dom";
import { AnimatePresence, motion } from "motion/react";
import { api } from "./lib/api";
import Dashboard from "./pages/Dashboard";
import Connect from "./pages/Connect";
import Peers from "./pages/Peers";
import Mining from "./pages/Mining";
import Settings from "./pages/Settings";
import Logs from "./pages/Logs";
import Onboarding from "./pages/Onboarding";
import ClientStatus from "./components/ClientStatus";
import logo from "./assets/logo.svg";

export default function App() {
  const [bootChecked, setBootChecked] = useState(false);
  const [needsBootstrap, setNeedsBootstrap] = useState(false);
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    api
      .bootstrapNeeded()
      .then((needs) => {
        if (cancelled) return;
        setNeedsBootstrap(needs);
        setBootChecked(true);
        if (needs) navigate("/onboarding", { replace: true });
      })
      .catch(() => {
        // Backend not yet ready (dev mode reload). Try again shortly.
        setTimeout(() => setBootChecked(false), 250);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (!bootChecked) {
    return <SplashScreen />;
  }

  if (needsBootstrap) {
    return (
      <Routes>
        <Route
          path="/onboarding"
          element={
            <Onboarding
              onFinished={() => {
                setNeedsBootstrap(false);
                navigate("/", { replace: true });
              }}
            />
          }
        />
        <Route path="*" element={<Navigate to="/onboarding" replace />} />
      </Routes>
    );
  }

  return (
    <div className="flex flex-col h-full">
      <TopBar />
      <RoutedContent />
    </div>
  );
}

function RoutedContent() {
  const location = useLocation();
  return (
    <main className="flex-1 overflow-y-auto px-12 py-14">
      <AnimatePresence mode="wait">
        <motion.div
          key={location.pathname}
          initial={{ opacity: 0, y: 6 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0 }}
          transition={{ duration: 0.15, ease: [0.16, 1, 0.3, 1] }}
        >
          <Routes location={location}>
            <Route path="/" element={<Dashboard />} />
            <Route path="/connect" element={<Connect />} />
            <Route path="/peers" element={<Peers />} />
            <Route path="/mining" element={<Mining />} />
            <Route path="/logs" element={<Logs />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </motion.div>
      </AnimatePresence>
    </main>
  );
}

function TopBar() {
  return (
    <motion.header
      className="shrink-0 border-b border-border bg-bg sticky top-0 z-50"
      initial={{ opacity: 0, y: -12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.6, ease: [0.16, 1, 0.3, 1] }}
    >
      <div className="max-w-[1400px] mx-auto px-10 h-20 flex items-center justify-between gap-10">
        {/* Brand */}
        <a
          href="#/"
          className="group flex items-center gap-3 transition-opacity hover:opacity-90"
        >
          <img
            src={logo}
            className="h-9 w-9 transition-transform duration-500 ease-out group-hover:rotate-[8deg]"
            alt="Parallax"
          />
          <span className="text-xl font-medium tracking-tight text-fg">
            Parallax
          </span>
        </a>

        {/* Centered nav */}
        <nav className="flex items-center gap-1">
          <TopLink to="/">Client</TopLink>
          <TopLink to="/connect">Connect</TopLink>
          <TopLink to="/peers">Peers</TopLink>
          <TopLink to="/mining">Mining</TopLink>
          <TopLink to="/settings">Settings</TopLink>
        </nav>

        {/* Right-side live client status */}
        <ClientStatus />
      </div>
    </motion.header>
  );
}

function TopLink({ to, children }: { to: string; children: React.ReactNode }) {
  return (
    <NavLink
      to={to}
      end={to === "/"}
      className={({ isActive }) =>
        `top-nav-link ${isActive ? "top-nav-link-active" : ""}`
      }
    >
      {children}
    </NavLink>
  );
}

function SplashScreen() {
  return (
    <div className="h-full grid place-items-center">
      <motion.div
        className="text-center"
        initial={{ opacity: 0, scale: 0.96 }}
        animate={{ opacity: 1, scale: 1 }}
        transition={{ duration: 0.6, ease: [0.16, 1, 0.3, 1] }}
      >
        <img src={logo} className="h-20 w-20 mx-auto mb-6" alt="Parallax" />
        <div className="font-serif text-2xl text-fg">Parallax</div>
        <div className="eyebrow mt-2">Loading</div>
      </motion.div>
    </div>
  );
}
