import React from "react";
import { notFound } from "next/navigation";
import { SiteShell } from "@/features/site";
import { loadSiteContext } from "@/features/site/siteContext.server";

interface SiteHostLayoutProps {
  children: React.ReactNode;
  params: Promise<{ host: string }>;
}

/** Every page of a shop's site (catalog, cart, checkout, account…) renders inside its themed shell. */
export default async function SiteHostLayout({ children, params }: SiteHostLayoutProps) {
  const { host } = await params;
  const ctx = await loadSiteContext(host);
  if (!ctx) notFound();

  return (
    <SiteShell
      config={ctx.templateConfig}
      shopName={ctx.payload.shop.name}
      shopId={ctx.payload.site.shop_id}
      platformOrigin={ctx.platformOrigin}
    >
      {children}
    </SiteShell>
  );
}
