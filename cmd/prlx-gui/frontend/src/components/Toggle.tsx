import { motion } from "motion/react";

/**
 * Toggle is a small spring-animated switch. Used in Settings and the
 * onboarding wizard for any boolean control.
 *
 * The knob is positioned with plain CSS (left: 2px) and animated via a
 * transform `x`. We deliberately don't tween `left` between `0.125rem` and
 * `calc(100% - 1.375rem)` because motion can't interpolate `calc()`
 * expressions — the first transition would start from `left: auto` (the
 * right edge of the viewport) and snap from off-screen into place.
 */
export default function Toggle({
  checked,
  onChange,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  // Container is 44px wide (h-6 w-11). Knob is 20px (h-5 w-5). With a 2px
  // inset on each side, the knob slides exactly 20px from off → on.
  const offset = 20;

  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      className={`relative inline-flex h-6 w-11 rounded-full transition-colors duration-300 ${
        checked ? "bg-gold" : "bg-bg-elev-2 border border-border-strong"
      }`}
    >
      <motion.span
        className="absolute top-0.5 left-0.5 h-5 w-5 rounded-full bg-fg shadow-md"
        animate={{ x: checked ? offset : 0 }}
        transition={{ type: "spring", stiffness: 500, damping: 30 }}
      />
    </button>
  );
}
