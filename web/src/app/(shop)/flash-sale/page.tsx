import React, { Suspense } from "react";
import { StateView } from "@/components";
import { loadFlashSales } from "@/lib/catalog";
import { FlashSaleClient } from "./FlashSaleClient";

export const dynamic = "force-dynamic";

export default async function FlashSalePage() {
  const data = await loadFlashSales();

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải khung giờ Flash sale..." rows={6} />
        </div>
      }
    >
      <FlashSaleClient
        slots={data.slots}
        products={data.products}
        shops={data.shops}
        loadError={data.error}
        serverNowMs={Date.now()}
      />
    </Suspense>
  );
}
