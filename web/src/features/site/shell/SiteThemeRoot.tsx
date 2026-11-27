"use client";

import React, { useMemo } from "react";
import { UiScope } from "@/components/ui/ui-scope";
import type { SiteTheme } from "@/lib/site/types";
import type {
  SiteArchetype,
  SiteCardStyle,
  SiteContainerWidth,
  SiteDensity,
  SiteFooterStyle,
  SiteHeaderStyle,
  SiteRadius,
} from "../templateTypes";
import { buildSiteShellStyle } from "./siteStyleVars";

export interface SiteThemeRootProps {
  theme: Pick<
    SiteTheme,
    | "bg_color"
    | "surface_color"
    | "text_color"
    | "accent_color"
    | "accent_text_color"
    | "font_family"
  > & {
    radius?: SiteRadius;
    density?: SiteDensity;
  };
  archetype: SiteArchetype;
  headerStyle: SiteHeaderStyle;
  footerStyle: SiteFooterStyle;
  containerWidth: SiteContainerWidth;
  cardStyle: SiteCardStyle;
  radius: SiteRadius;
  density: SiteDensity;
  previewMode: boolean;
  className?: string;
  children: React.ReactNode;
}

/**
 * Client-side `UiScope` host that derives scoped `--site-*` and `--ui-*` custom properties
 * from compact theme primitives so SSR HTML includes full theme styles without inflating
 * the RSC Flight payload of `SiteShell` across chunk boundaries.
 */
export function SiteThemeRoot({
  theme,
  archetype,
  headerStyle,
  footerStyle,
  containerWidth,
  cardStyle,
  radius,
  density,
  previewMode,
  className,
  children,
}: SiteThemeRootProps) {
  const { isDark, style } = useMemo(
    () =>
      buildSiteShellStyle({
        theme,
        layout: { container_width: containerWidth },
      }),
    [theme, containerWidth]
  );

  return (
    <UiScope
      className={className}
      data-site-root=""
      data-site-theme-mode={isDark ? "dark" : "light"}
      data-site-preview={previewMode ? "true" : "false"}
      data-site-archetype={archetype}
      data-site-header={headerStyle}
      data-site-footer={footerStyle}
      data-site-container={containerWidth}
      data-site-card={cardStyle}
      data-site-radius={radius}
      data-site-density={density}
      style={style}
    >
      {children}
    </UiScope>
  );
}
