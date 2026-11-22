import React from "react";
import { loadHomeCatalogData } from "@/lib/catalog";
import { HomeClient } from "./HomeClient";

export const dynamic = "force-dynamic";

export default async function HomePage() {
  const data = await loadHomeCatalogData();

  return (
    <HomeClient
      categories={data.categories}
      shops={data.shops}
      products={data.products}
      flashSlots={data.flashSlots}
      serverNowMs={data.serverNowMs}
      error={data.error}
    />
  );
}
