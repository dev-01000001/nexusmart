import React, { Suspense } from "react";
import { StateView } from "@/components";
import { loadFlashSalePageData } from "@/lib/catalog";
import { FlashSaleClient } from "./FlashSaleClient";

export const dynamic = "force-dynamic";

export default async function FlashSalePage() {
  const data = await loadFlashSalePageData();

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải khung giờ Flash sale..." rows={6} />
        </div>
      }
    >
      <FlashSaleClient
        flashSlots={data.flashSlots}
        shops={data.shops}
        serverNowMs={data.serverNowMs}
        error={data.error}
      />
    </Suspense>
  );
}
