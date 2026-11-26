import type React from "react";
import { buildSiteThemeVars, buildSiteUiVars, isDarkHex, siteRadiusToCss } from "@/lib/site/theme";
import type {
  SectionPadding,
  SectionSurface,
  SiteArchetype,
  SiteContainerWidth,
  SiteDensity,
  SiteTemplateConfig,
} from "../templateTypes";

const CONTAINER_MAX_WIDTH: Record<SiteContainerWidth, string> = {
  narrow: "1040px",
  standard: "1260px",
  wide: "1440px",
  full: "100%",
};

const DENSITY_SECTION_GAP: Record<Exclude<SiteDensity, "">, string> = {
  compact: "clamp(22px, 2.8vw, 38px)",
  balanced: "clamp(40px, 5vw, 72px)",
  airy: "clamp(56px, 7vw, 104px)",
};

const DENSITY_GUTTER: Record<Exclude<SiteDensity, "">, string> = {
  compact: "clamp(10px, 1.2vw, 14px)",
  balanced: "clamp(16px, 2vw, 24px)",
  airy: "clamp(22px, 2.8vw, 36px)",
};

export interface SiteShellStyleInput {
  theme: Pick<
    SiteTemplateConfig["theme"],
    | "bg_color"
    | "surface_color"
    | "text_color"
    | "accent_color"
    | "accent_text_color"
    | "font_family"
    | "radius"
    | "density"
  >;
  layout: Pick<SiteTemplateConfig["layout"], "container_width">;
}

/**
 * Builds the scoped CSS custom property bag for `SiteShell`'s `UiScope` root:
 * - all `--ui-*` tokens from `buildSiteUiVars(theme)` (with flat resting shadows per §11.1);
 * - all `--site-*` tokens from `buildSiteThemeVars(theme)` + density/container spacing;
 * - legacy `tokens.css` remappings so sibling CSS-Module routes still inherit dark/light site themes.
 */
export function buildSiteShellStyle(tpl: SiteShellStyleInput): {
  isDark: boolean;
  style: React.CSSProperties;
} {
  const themeVars = buildSiteThemeVars(tpl.theme as SiteTemplateConfig["theme"]);
  const uiVars = buildSiteUiVars(tpl.theme as SiteTemplateConfig["theme"]);
  const isDark = isDarkHex(themeVars["--site-bg"]);
  const radiusCss = siteRadiusToCss(tpl.theme.radius);
  const densityKey: Exclude<SiteDensity, ""> =
    tpl.theme.density === "compact" || tpl.theme.density === "airy"
      ? tpl.theme.density
      : "balanced";
  const maxW = CONTAINER_MAX_WIDTH[tpl.layout.container_width] ?? "1260px";
  const sectionGap = DENSITY_SECTION_GAP[densityKey];
  const gutter = DENSITY_GUTTER[densityKey];

  const style: Record<string, string> = {
    ...themeVars,
    ...uiVars,
    // Storefront stays flat at rest; menus/dialogs/sheets retain --ui-shadow-overlay.
    "--ui-shadow-card": "none",
    "--ui-shadow-button": "none",
    // Density & container geometry (§11.1)
    "--site-max-w": maxW,
    "--site-section-gap": sectionGap,
    "--site-gutter": gutter,
    "--site-pad-y": sectionGap,
    "--site-gap": gutter,
    "--site-radius": radiusCss,
    "--site-btn-radius": radiusCss,
    // Legacy token remapping for sibling unconverted CSS-Module pages (S4b/S4c/S4d)
    "--bg": themeVars["--site-bg"],
    "--surface": themeVars["--site-surface-alt"],
    "--bg-0": themeVars["--site-surface"],
    "--bg-1": themeVars["--site-bg"],
    "--bg-2": themeVars["--site-surface-alt"],
    "--surface-white": themeVars["--site-surface"],
    "--surface-subtle": themeVars["--site-surface-alt"],
    "--surface-muted": themeVars["--site-divider"],
    "--card": themeVars["--site-surface"],
    "--raised": themeVars["--site-surface-alt"],
    "--border": themeVars["--site-divider"],
    "--rule": themeVars["--site-divider"],
    "--control": uiVars["--ui-input"],
    "--ink": themeVars["--site-ink"],
    "--ink-1": themeVars["--site-ink"],
    "--ink-2": themeVars["--site-ink"],
    "--ink-3": themeVars["--site-muted"],
    "--ink-strong": themeVars["--site-ink"],
    "--ink-secondary": themeVars["--site-ink"],
    "--muted": themeVars["--site-muted"],
    "--accent": themeVars["--site-accent"],
    "--accent-ink": themeVars["--site-accent-ink"],
    "--accent-tint": themeVars["--site-accent-soft"],
    "--accent-tint-ink": themeVars["--site-accent-soft-ink"],
    "--band-dark": themeVars["--site-band-bg"],
    "--band-dark-ink": themeVars["--site-band-ink"],
    "--primary-ink-bg": themeVars["--site-ink"],
    "--primary-ink-fg": themeVars["--site-bg"],
    "--radius-shop": radiusCss,
    "--radius-card": radiusCss,
    "--radius-control": radiusCss,
    "--radius-chip": radiusCss,
    "--font-sans": themeVars["--site-font"],
    colorScheme: isDark ? "dark" : "light",
  };

  if (isDark) {
    style["--positive"] = "#6ee7b7";
    style["--negative"] = "#fca5a5";
    style["--amber"] = "#fcd34d";
    style["--amber-ink"] = "#fde68a";
    style["--pill-positive-bg"] = "#12382b";
    style["--pill-positive-ink"] = "#a7f3d0";
    style["--pill-negative-bg"] = "#451a1a";
    style["--pill-negative-ink"] = "#fecaca";
    style["--pill-warning-bg"] = "#422c0c";
    style["--pill-warning-ink"] = "#fde68a";
    style["--pill-neutral-bg"] = themeVars["--site-surface-alt"];
    style["--pill-neutral-ink"] = themeVars["--site-ink"];
  }

  return { isDark, style: style as unknown as React.CSSProperties };
}

export function containerWidthClass(width: SiteContainerWidth): string {
  switch (width) {
    case "narrow":
      return "mx-auto w-full max-w-[1040px] min-w-0";
    case "wide":
      return "mx-auto w-full max-w-[1440px] min-w-0";
    case "full":
      return "mx-auto w-full max-w-full min-w-0";
    case "standard":
    default:
      return "mx-auto w-full max-w-[1260px] min-w-0";
  }
}

export function sectionSurfaceClass(surface: SectionSurface): string {
  switch (surface) {
    case "surface":
      return "bg-card text-card-foreground";
    case "band":
      return "bg-[var(--site-band-bg)] text-[var(--site-band-ink)]";
    case "accent-soft":
      return "bg-[var(--site-accent-soft)] text-[var(--site-accent-soft-ink)]";
    case "bg":
    default:
      return "bg-background text-foreground";
  }
}

export function sectionPaddingClass(padding?: SectionPadding): string {
  switch (padding) {
    case "compact":
      return "py-[clamp(20px,2.5vw,34px)]";
    case "normal":
      return "py-[clamp(38px,4.8vw,64px)]";
    case "generous":
      return "py-[clamp(56px,7vw,100px)]";
    default:
      return "py-[var(--site-section-gap,clamp(40px,5vw,72px))]";
  }
}

export function archetypeTitleTone(archetype: SiteArchetype): string {
  switch (archetype) {
    case "editorial":
      return "font-bold tracking-[-0.025em] leading-[1.06]";
    case "sport-active":
      return "font-extrabold tracking-[-0.025em] leading-[1.02]";
    case "luxury-maison":
      return "font-normal tracking-[0.02em] leading-[1.15]";
    case "craft-atelier":
      return "font-semibold tracking-[-0.015em] leading-[1.1]";
    case "one-page-launch":
      return "font-bold tracking-[-0.02em] leading-[1.08]";
    case "dense-grid":
    case "multi-sku-catalog":
      return "font-semibold tracking-[-0.015em] leading-[1.14]";
    case "minimal-fashion":
    case "classic":
    default:
      return "font-semibold tracking-[-0.02em] leading-[1.12]";
  }
}

export const SITE_PX = "px-[clamp(1rem,2.2vw,2rem)]";
