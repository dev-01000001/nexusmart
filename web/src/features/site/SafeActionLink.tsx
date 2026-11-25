import React from "react";
import Link from "next/link";
import { sanitizeSafeHref } from "@/lib/site/theme";

export interface SafeActionLinkProps {
  href: string;
  label: string;
  className: string;
  previewMode?: boolean;
  fallbackHref?: string;
}

/**
 * Renders an internal Next.js Link or an external anchor with `rel="noopener noreferrer"`
 * after validating the URL scheme. Renders a non-navigating span in Site Editor preview mode.
 */
export function SafeActionLink({
  href,
  label,
  className,
  previewMode = false,
  fallbackHref = "",
}: SafeActionLinkProps) {
  const cleanHref = sanitizeSafeHref(href, fallbackHref);
  if (!label.trim() || !cleanHref) return null;
  if (previewMode) {
    return <span className={className}>{label}</span>;
  }
  if (cleanHref.startsWith("/")) {
    return (
      <Link href={cleanHref} className={className}>
        {label}
      </Link>
    );
  }
  return (
    <a href={cleanHref} className={className} rel="noopener noreferrer">
      {label}
    </a>
  );
}
