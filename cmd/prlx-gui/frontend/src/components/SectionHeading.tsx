import { motion } from "motion/react";

/**
 * Page-level section heading. Mirrors the marketing site's pattern:
 *
 *   ─                          ← short gold accent rule
 *   OVERVIEW                   ← uppercase letter-spaced eyebrow
 *   Your node, at a glance.    ← Newsreader serif display headline
 *   Optional muted subtitle.
 *
 * Used as the header of every page so the visual rhythm stays consistent.
 */
export default function SectionHeading({
  eyebrow,
  title,
  subtitle,
  align = "left",
  trailing,
}: {
  eyebrow: string;
  title: string;
  subtitle?: string;
  align?: "left" | "center";
  trailing?: React.ReactNode;
}) {
  return (
    <header
      className={`${
        align === "center"
          ? "flex flex-col items-center text-center"
          : "flex items-end justify-between gap-6"
      }`}
    >
      <div className={align === "center" ? "max-w-2xl" : ""}>
        <motion.span
          className="block h-[2px] w-10 bg-gold rounded-full mb-5"
          initial={{ scaleX: 0 }}
          animate={{ scaleX: 1 }}
          transition={{ duration: 0.4, ease: [0.16, 1, 0.3, 1] }}
          style={{
            transformOrigin: align === "center" ? "center" : "left center",
            marginInline: align === "center" ? "auto" : undefined,
          }}
        />
        <div className="eyebrow mb-3">{eyebrow}</div>
        <h1 className="display">{title}</h1>
        {subtitle && (
          <p className="text-muted mt-4 max-w-2xl">{subtitle}</p>
        )}
      </div>

      {trailing && align !== "center" && <div>{trailing}</div>}
    </header>
  );
}
