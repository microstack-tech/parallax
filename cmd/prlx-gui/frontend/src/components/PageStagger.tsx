import { motion, Variants } from "motion/react";

/**
 * PageStagger orchestrates the entrance choreography of an entire page.
 * Children that opt in by using <StaggerItem> reveal one after another in
 * the order they appear, producing the cinematic "the page is settling
 * into place" feel that the marketing site uses on hero sections.
 */
const containerVariants: Variants = {
  hidden: {},
  visible: {
    transition: {
      staggerChildren: 0.04,
      delayChildren: 0,
    },
  },
};

const itemVariants: Variants = {
  hidden: { opacity: 0, y: 10 },
  visible: {
    opacity: 1,
    y: 0,
    transition: { duration: 0.28, ease: [0.16, 1, 0.3, 1] },
  },
};

export function PageStagger({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <motion.div
      variants={containerVariants}
      initial="hidden"
      animate="visible"
      className={className}
    >
      {children}
    </motion.div>
  );
}

export function StaggerItem({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <motion.div variants={itemVariants} className={className}>
      {children}
    </motion.div>
  );
}
