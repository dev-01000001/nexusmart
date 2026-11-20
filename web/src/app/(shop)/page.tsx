import React from "react";
import { loadStorefront } from "@/lib/catalog";
import { HomeClient } from "./HomeClient";

export const dynamic = "force-dynamic";

export default async function HomePage() {
  const data = await loadStorefront();

  return (
    <HomeClient
      categories={data.categories}
      shops={data.shops}
      products={data.products}
      flashSlots={data.flashSlots}
      vouchers={data.vouchers}
      loadError={data.error}
      serverNowMs={Date.now()}
    />
  );
}
