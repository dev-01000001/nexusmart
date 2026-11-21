import type { Metadata } from "next";
import React, { Suspense } from "react";
import { StateView } from "@/components/StateView";
import { loadSiteDirectory } from "@/features/platform/directory.server";
import { AboutClient } from "./AboutClient";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Về NexusMart",
  description:
    "Tìm hiểu cách nền tảng NexusMart trao cho mỗi thương hiệu và xưởng sản xuất một cửa hàng độc lập trên tên miền phụ riêng.",
};

export default async function AboutNexusMartPage() {
  const directory = await loadSiteDirectory(1, 24);

  return (
    <Suspense
      fallback={
        <div style={{ padding: "32px var(--margin)" }}>
          <StateView kind="loading" title="Đang tải trang Về NexusMart..." rows={5} />
        </div>
      }
    >
      <AboutClient directory={directory} />
    </Suspense>
  );
}
