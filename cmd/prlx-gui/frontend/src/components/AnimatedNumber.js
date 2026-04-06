import { jsx as _jsx } from "react/jsx-runtime";
import { useEffect, useRef } from "react";
import { animate, useMotionValue, useTransform, motion } from "motion/react";
/**
 * AnimatedNumber smoothly tweens between values when `value` changes.
 * Used for stat tiles so block height, peer count, etc. count up rather
 * than snapping. Falls back gracefully for very large numbers (block
 * heights) by clamping the animation duration.
 */
export default function AnimatedNumber({ value, format, className, }) {
    const motionVal = useMotionValue(value);
    const display = useTransform(motionVal, (v) => format ? format(v) : Math.round(v).toLocaleString());
    const prev = useRef(value);
    useEffect(() => {
        const from = prev.current;
        const to = value;
        if (from === to)
            return;
        // Cap duration so jumping by millions of blocks doesn't crawl.
        const delta = Math.abs(to - from);
        const duration = Math.min(1.2, Math.max(0.4, delta / 5000));
        const controls = animate(motionVal, to, {
            duration,
            ease: [0.16, 1, 0.3, 1],
        });
        prev.current = to;
        return () => controls.stop();
    }, [value, motionVal]);
    return _jsx(motion.span, { className: className, children: display });
}
