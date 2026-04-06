import { jsx as _jsx } from "react/jsx-runtime";
import { motion } from "motion/react";
/**
 * PageStagger orchestrates the entrance choreography of an entire page.
 * Children that opt in by using <StaggerItem> reveal one after another in
 * the order they appear, producing the cinematic "the page is settling
 * into place" feel that the marketing site uses on hero sections.
 */
const containerVariants = {
    hidden: {},
    visible: {
        transition: {
            staggerChildren: 0.04,
            delayChildren: 0,
        },
    },
};
const itemVariants = {
    hidden: { opacity: 0, y: 10 },
    visible: {
        opacity: 1,
        y: 0,
        transition: { duration: 0.28, ease: [0.16, 1, 0.3, 1] },
    },
};
export function PageStagger({ children, className, }) {
    return (_jsx(motion.div, { variants: containerVariants, initial: "hidden", animate: "visible", className: className, children: children }));
}
export function StaggerItem({ children, className, }) {
    return (_jsx(motion.div, { variants: itemVariants, className: className, children: children }));
}
