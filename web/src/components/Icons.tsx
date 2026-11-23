import React from "react";

export interface IconProps {
  size?: number;
  className?: string;
}

const baseProps = (size: number, className?: string) => ({
  width: size,
  height: size,
  viewBox: "0 0 20 20",
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 1.5,
  strokeLinecap: "round" as const,
  strokeLinejoin: "round" as const,
  className,
  "aria-hidden": true,
});

/**
 * Minimal woven-thread monogram mark for NexusMart (never a black square with a letter).
 */
export function NexusMark({
  size = 20,
  className,
  accentColor = "var(--accent)",
}: IconProps & { accentColor?: string }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      className={className}
      aria-hidden="true"
    >
      <path
        d="M5 19V5L14.5 19V5"
        stroke="currentColor"
        strokeWidth="2.1"
        strokeLinecap="square"
        strokeLinejoin="miter"
      />
      <path d="M19 5V19" stroke={accentColor} strokeWidth="2.1" strokeLinecap="square" />
    </svg>
  );
}

export function IconSearch({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <circle cx="9" cy="9" r="5.5" />
      <path d="M13.2 13.2L17 17" />
    </svg>
  );
}

export function IconBag({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M4.5 7H15.5L14.7 16.5H5.3L4.5 7Z" />
      <path d="M7.5 7V5.5C7.5 4.1 8.6 3 10 3C11.4 3 12.5 4.1 12.5 5.5V7" />
    </svg>
  );
}

export function IconChat({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M4 5.5C4 4.7 4.7 4 5.5 4H14.5C15.3 4 16 4.7 16 5.5V12.5C16 13.3 15.3 14 14.5 14H8L4.5 16.5V14H5.5C4.7 14 4 13.3 4 12.5V5.5Z" />
    </svg>
  );
}

export function IconSpark({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M10 3L11.6 8.4L17 10L11.6 11.6L10 17L8.4 11.6L3 10L8.4 8.4L10 3Z" />
    </svg>
  );
}

export function IconUser({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <circle cx="10" cy="6.8" r="3" />
      <path d="M4.5 16.5C5.2 13.8 7.3 12.2 10 12.2C12.7 12.2 14.8 13.8 15.5 16.5" />
    </svg>
  );
}

export function IconMenu({ size = 18, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M3.5 6H16.5" />
      <path d="M3.5 10H16.5" />
      <path d="M3.5 14H16.5" />
    </svg>
  );
}

export function IconClose({ size = 18, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M5 5L15 15" />
      <path d="M15 5L5 15" />
    </svg>
  );
}

export function IconChevronRight({ size = 14, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M7.5 4.5L13 10L7.5 15.5" />
    </svg>
  );
}

export function IconChevronDown({ size = 14, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M5 7.5L10 12.5L15 7.5" />
    </svg>
  );
}

export function IconCheck({ size = 14, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M4.5 10.5L8.2 14.2L15.5 6.5" />
    </svg>
  );
}

export function IconPlus({ size = 14, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M10 4.5V15.5" />
      <path d="M4.5 10H15.5" />
    </svg>
  );
}

export function IconMinus({ size = 14, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M4.5 10H15.5" />
    </svg>
  );
}

export function IconTrash({ size = 15, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M4.5 6H15.5" />
      <path d="M8 6V4.5H12V6" />
      <path d="M6 6L6.6 16H13.4L14 6" />
    </svg>
  );
}

export function IconDashboard({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <rect x="3.5" y="3.5" width="5.5" height="5.5" rx="1" />
      <rect x="11" y="3.5" width="5.5" height="5.5" rx="1" />
      <rect x="3.5" y="11" width="5.5" height="5.5" rx="1" />
      <rect x="11" y="11" width="5.5" height="5.5" rx="1" />
    </svg>
  );
}

export function IconPackage({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M10 3L16 6.3V13.7L10 17L4 13.7V6.3L10 3Z" />
      <path d="M4 6.3L10 9.6L16 6.3" />
      <path d="M10 9.6V17" />
    </svg>
  );
}

export function IconStore({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M3.5 8L4.8 4H15.2L16.5 8" />
      <path d="M3.5 8C3.5 9.2 4.5 10 5.7 10C6.9 10 7.9 9.2 7.9 8C7.9 9.2 8.9 10 10 10C11.1 10 12.1 9.2 12.1 8C12.1 9.2 13.1 10 14.3 10C15.5 10 16.5 9.2 16.5 8" />
      <path d="M4.8 10V16H15.2V10" />
    </svg>
  );
}

export function IconTag({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M3.5 10.2V4.5H9.2L16.2 11.5L10.5 17.2L3.5 10.2Z" />
      <circle cx="7" cy="7.5" r="1" />
    </svg>
  );
}

export function IconWallet({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <rect x="3.5" y="5" width="13" height="10.5" rx="1.5" />
      <path d="M12.5 10.2H16.5" />
      <circle cx="13.2" cy="10.2" r="0.6" fill="currentColor" />
    </svg>
  );
}

export function IconSliders({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M4 6H16" />
      <path d="M4 10H16" />
      <path d="M4 14H16" />
      <circle cx="7.5" cy="6" r="1.5" fill="var(--bg)" />
      <circle cx="12.5" cy="10" r="1.5" fill="var(--bg)" />
      <circle cx="8.5" cy="14" r="1.5" fill="var(--bg)" />
    </svg>
  );
}

export function IconShield({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M10 3L15.5 5.3V10.2C15.5 13.5 13.2 15.8 10 17C6.8 15.8 4.5 13.5 4.5 10.2V5.3L10 3Z" />
      <path d="M7.5 10L9.2 11.7L12.8 8.2" />
    </svg>
  );
}

export function IconAlert({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <circle cx="10" cy="10" r="6.5" />
      <path d="M10 6.8V10.4" />
      <circle cx="10" cy="13.2" r="0.6" fill="currentColor" />
    </svg>
  );
}

export function IconClock({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <circle cx="10" cy="10" r="6.5" />
      <path d="M10 6.5V10L12.5 11.5" />
    </svg>
  );
}

export function IconFileText({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M5.5 3.5H12L15 6.5V16.5H5.5V3.5Z" />
      <path d="M8 9.5H12.5" />
      <path d="M8 12.5H12.5" />
    </svg>
  );
}

export function IconTruck({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M3 5.5H12V13.5H3V5.5Z" />
      <path d="M12 8H15.2L17 10.5V13.5H12" />
      <circle cx="6.2" cy="14.5" r="1.5" />
      <circle cx="14.2" cy="14.5" r="1.5" />
    </svg>
  );
}

export function IconArrowUpRight({ size = 14, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M6 14L14 6" />
      <path d="M7.5 6H14V12.5" />
    </svg>
  );
}

export function IconRefresh({ size = 15, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M15.5 10A5.5 5.5 0 1 1 13.8 6" />
      <path d="M14.5 3.5V6.5H11.5" />
    </svg>
  );
}

export function IconHeart({
  size = 16,
  className,
  filled = false,
}: IconProps & { filled?: boolean }) {
  return (
    <svg {...baseProps(size, className)} fill={filled ? "currentColor" : "none"}>
      <path d="M10 16.2L4.4 10.9C2.9 9.5 2.9 7.1 4.4 5.7C5.8 4.4 8 4.6 9.3 6L10 6.8L10.7 6C12 4.6 14.2 4.4 15.6 5.7C17.1 7.1 17.1 9.5 15.6 10.9L10 16.2Z" />
    </svg>
  );
}

export function IconStar({
  size = 14,
  className,
  filled = false,
}: IconProps & { filled?: boolean }) {
  return (
    <svg {...baseProps(size, className)} fill={filled ? "currentColor" : "none"}>
      <path d="M10 3.2L12 7.4L16.6 8L13.2 11.2L14.1 15.8L10 13.6L5.9 15.8L6.8 11.2L3.4 8L8 7.4L10 3.2Z" />
    </svg>
  );
}

export function IconGift({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <rect x="3.5" y="8" width="13" height="8.5" rx="0.5" />
      <path d="M3 5.5H17V8H3V5.5Z" />
      <path d="M10 5.5V16.5" />
      <path d="M10 5.5C9.2 3.8 7.4 3.2 6.4 4.1C5.6 4.8 6.4 5.5 10 5.5Z" />
      <path d="M10 5.5C10.8 3.8 12.6 3.2 13.6 4.1C14.4 4.8 13.6 5.5 10 5.5Z" />
    </svg>
  );
}

export function IconPlay({ size = 15, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M6.5 4.8L15 10L6.5 15.2V4.8Z" fill="currentColor" />
    </svg>
  );
}

export function IconPhone({ size = 15, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M5.5 3.5H8L9.2 6.8L7.6 8C8.4 9.6 9.8 11 11.4 11.8L12.6 10.2L16 11.4V14C16 15.1 15.1 16 14 16C8.5 15.7 4.3 11.5 4 6C4 4.6 4.6 3.5 5.5 3.5Z" />
    </svg>
  );
}

export function IconMail({ size = 15, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <rect x="3" y="5" width="14" height="10" rx="1" />
      <path d="M3.5 5.8L10 10.5L16.5 5.8" />
    </svg>
  );
}

export function IconHome({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M3.5 9.5L10 4L16.5 9.5" />
      <path d="M5.5 8.5V16H14.5V8.5" />
    </svg>
  );
}

export function IconBolt({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M11 3L5.5 10.8H10L9 17L14.5 9.2H10L11 3Z" />
    </svg>
  );
}

export function IconBell({ size = 16, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M10 3.2C7.4 3.2 5.5 5.1 5.5 7.7V11L4 13.5H16L14.5 11V7.7C14.5 5.1 12.6 3.2 10 3.2Z" />
      <path d="M8.3 15.5C8.7 16.3 9.3 16.8 10 16.8C10.7 16.8 11.3 16.3 11.7 15.5" />
    </svg>
  );
}

export function IconDownload({ size = 15, className }: IconProps) {
  return (
    <svg {...baseProps(size, className)}>
      <path d="M10 3.5V12.5" />
      <path d="M6.5 9.5L10 13L13.5 9.5" />
      <path d="M4 15.5H16" />
    </svg>
  );
}
