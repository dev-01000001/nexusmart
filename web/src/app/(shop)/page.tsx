import React from "react";
import { directoryContext, loadSiteDirectory } from "@/features/platform/directory.server";
import { parseDirectoryPage } from "@/features/platform/directoryModel";
import { PlatformHome } from "@/features/platform/PlatformHome";

export const dynamic = "force-dynamic";

interface HomePageProps {
  searchParams?: Promise<{ trang?: string | string[] }>;
}

export default async function HomePage({ searchParams }: HomePageProps) {
  const params = searchParams ? await searchParams : {};
  const page = parseDirectoryPage(params.trang);
  const ctx = await directoryContext();
  const directory = await loadSiteDirectory(page, undefined, ctx);

  return <PlatformHome directory={directory} rootDomain={ctx.rootDomain} />;
}
