import type { Config } from "tailwindcss";

// Tokens ported verbatim from ../parallax-website/app/globals.css (.dark
// scope). The website uses gold (#f7931a) as the dominant accent and a
// warm-tinted near-black background; purple is reserved for very rare
// emphasis. Keep this file the single source of design truth — every page
// pulls from here.
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        bg: "oklch(0.06 0.015 265 / <alpha-value>)",
        "bg-elev": "oklch(0.12 0.012 265 / <alpha-value>)",
        "bg-elev-2": "oklch(0.14 0.015 265 / <alpha-value>)",
        fg: "oklch(0.93 0.005 80 / <alpha-value>)",
        muted: "oklch(0.6 0.01 265 / <alpha-value>)",
        border: "oklch(1 0 0 / 0.08)",
        "border-strong": "oklch(1 0 0 / 0.15)",

        // Gold is the hero accent — used on icons, featured-card left
        // borders, primary CTAs, and any "earned/value" semantics.
        gold: "rgb(247 147 26 / <alpha-value>)",
        "gold-muted": "rgb(247 147 26 / 0.12)",
        "gold-fg": "oklch(0.15 0.03 60 / <alpha-value>)",

        // Purple is kept around for rare focus rings / charts but is NOT
        // the default brand colour.
        primary: "oklch(0.5573 0.2543 283.67 / <alpha-value>)",
        "primary-fg": "oklch(0.985 0 0 / <alpha-value>)",
        "glow-purple": "oklch(0.5573 0.2543 283.67 / 0.20)",

        success: "oklch(0.696 0.17 162.48 / <alpha-value>)",
        danger: "oklch(0.704 0.191 22.216 / <alpha-value>)",
      },
      fontFamily: {
        sans: [
          "Geist",
          "ui-sans-serif",
          "system-ui",
          "-apple-system",
          "sans-serif",
        ],
        serif: ["Newsreader", "ui-serif", "Georgia", "serif"],
        mono: ["Geist Mono", "ui-monospace", "SFMono-Regular", "Menlo", "monospace"],
      },
      borderRadius: {
        DEFAULT: "0.625rem",
      },
      letterSpacing: {
        eyebrow: "0.18em",
      },
      boxShadow: {
        "gold-glow": "0 0 60px -10px rgb(247 147 26 / 0.35)",
        "gold-glow-lg": "0 0 100px -20px rgb(247 147 26 / 0.55)",
        "card-hover": "0 30px 60px -30px rgb(0 0 0 / 0.6), 0 0 0 1px oklch(1 0 0 / 0.04), 0 0 40px -10px rgb(247 147 26 / 0.20)",
      },
      keyframes: {
        "fade-up": {
          "0%": { opacity: "0", transform: "translateY(16px)" },
          "100%": { opacity: "1", transform: "translateY(0)" },
        },
        "fade-in": {
          "0%": { opacity: "0" },
          "100%": { opacity: "1" },
        },
        "pulse-soft": {
          "0%, 100%": { opacity: "1" },
          "50%": { opacity: "0.45" },
        },
        shimmer: {
          "0%": { transform: "translateX(-100%)" },
          "100%": { transform: "translateX(100%)" },
        },
        "draw-line": {
          "0%": { transform: "scaleX(0)" },
          "100%": { transform: "scaleX(1)" },
        },
        "row-flash": {
          "0%": { backgroundColor: "rgb(247 147 26 / 0.10)" },
          "100%": { backgroundColor: "rgb(247 147 26 / 0)" },
        },
      },
      animation: {
        "fade-up": "fade-up 0.7s cubic-bezier(0.16, 1, 0.3, 1) both",
        "fade-in": "fade-in 0.5s ease-out both",
        "pulse-soft": "pulse-soft 2.4s ease-in-out infinite",
        shimmer: "shimmer 2.2s linear infinite",
        "draw-line": "draw-line 0.8s cubic-bezier(0.16, 1, 0.3, 1) 0.1s both",
        "row-flash": "row-flash 1.6s ease-out both",
      },
      transitionTimingFunction: {
        "out-expo": "cubic-bezier(0.16, 1, 0.3, 1)",
      },
    },
  },
  plugins: [],
} satisfies Config;
