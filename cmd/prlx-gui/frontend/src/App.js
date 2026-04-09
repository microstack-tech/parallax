import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
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
            if (cancelled)
                return;
            setNeedsBootstrap(needs);
            setBootChecked(true);
            if (needs)
                navigate("/onboarding", { replace: true });
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
        return _jsx(SplashScreen, {});
    }
    if (needsBootstrap) {
        return (_jsxs(Routes, { children: [_jsx(Route, { path: "/onboarding", element: _jsx(Onboarding, { onFinished: () => {
                            setNeedsBootstrap(false);
                            navigate("/", { replace: true });
                        } }) }), _jsx(Route, { path: "*", element: _jsx(Navigate, { to: "/onboarding", replace: true }) })] }));
    }
    return (_jsxs("div", { className: "flex flex-col h-full", children: [_jsx(TopBar, {}), _jsx(RoutedContent, {})] }));
}
function RoutedContent() {
    const location = useLocation();
    return (_jsx("main", { className: "flex-1 overflow-y-auto px-12 py-14", children: _jsx(AnimatePresence, { mode: "wait", children: _jsx(motion.div, { initial: { opacity: 0, y: 6 }, animate: { opacity: 1, y: 0 }, exit: { opacity: 0 }, transition: { duration: 0.15, ease: [0.16, 1, 0.3, 1] }, children: _jsxs(Routes, { location: location, children: [_jsx(Route, { path: "/", element: _jsx(Dashboard, {}) }), _jsx(Route, { path: "/connect", element: _jsx(Connect, {}) }), _jsx(Route, { path: "/peers", element: _jsx(Peers, {}) }), _jsx(Route, { path: "/mining", element: _jsx(Mining, {}) }), _jsx(Route, { path: "/logs", element: _jsx(Logs, {}) }), _jsx(Route, { path: "/settings", element: _jsx(Settings, {}) }), _jsx(Route, { path: "*", element: _jsx(Navigate, { to: "/", replace: true }) })] }) }, location.pathname) }) }));
}
function TopBar() {
    return (_jsx(motion.header, { className: "shrink-0 border-b border-border bg-bg sticky top-0 z-50", initial: { opacity: 0, y: -12 }, animate: { opacity: 1, y: 0 }, transition: { duration: 0.6, ease: [0.16, 1, 0.3, 1] }, children: _jsxs("div", { className: "max-w-[1400px] mx-auto px-10 h-20 flex items-center justify-between gap-10", children: [_jsxs("a", { href: "#/", className: "group flex items-center gap-3 transition-opacity hover:opacity-90", children: [_jsx("img", { src: logo, className: "h-9 w-9 transition-transform duration-500 ease-out group-hover:rotate-[8deg]", alt: "Parallax" }), _jsx("span", { className: "text-xl font-medium tracking-tight text-fg", children: "Parallax" })] }), _jsxs("nav", { className: "flex items-center gap-1", children: [_jsx(TopLink, { to: "/", children: "Client" }), _jsx(TopLink, { to: "/connect", children: "Connect" }), _jsx(TopLink, { to: "/peers", children: "Peers" }), _jsx(TopLink, { to: "/mining", children: "Mining" }), _jsx(TopLink, { to: "/settings", children: "Settings" })] }), _jsx(ClientStatus, {})] }) }));
}
function TopLink({ to, children }) {
    return (_jsx(NavLink, { to: to, end: to === "/", className: ({ isActive }) => `top-nav-link ${isActive ? "top-nav-link-active" : ""}`, children: children }));
}
function SplashScreen() {
    return (_jsx("div", { className: "h-full grid place-items-center", children: _jsxs(motion.div, { className: "text-center", initial: { opacity: 0, scale: 0.96 }, animate: { opacity: 1, scale: 1 }, transition: { duration: 0.6, ease: [0.16, 1, 0.3, 1] }, children: [_jsx("img", { src: logo, className: "h-20 w-20 mx-auto mb-6", alt: "Parallax" }), _jsx("div", { className: "font-serif text-2xl text-fg", children: "Parallax" }), _jsx("div", { className: "eyebrow mt-2", children: "Loading" })] }) }));
}
