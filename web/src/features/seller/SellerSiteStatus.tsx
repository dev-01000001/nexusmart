"use client";

import React from "react";
import Link from "next/link";
import { AlertTriangle, ExternalLink, Globe, Sparkles } from "lucide-react";
import { StatusBadge, siteStatusTone } from "@/components/app";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { useSellerContext } from "./SellerShell";

/**
 * Compact storefront live status bar on Seller Overview.
 * Keeps published state quiet (1 line) and highlights unpublished/suspended states with 1 direct CTA.
 */
export function SellerSiteStatus() {
  const { site } = useSellerContext();

  if (site.state === "published") {
    return (
      <Card className="py-0" aria-labelledby="site-status-heading">
        <CardContent className="flex flex-col gap-3 py-3.5 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex min-w-0 flex-wrap items-center gap-2.5">
            <StatusBadge tone={siteStatusTone("PUBLISHED")}>Đang mở bán</StatusBadge>
            <div className="flex min-w-0 items-center gap-1.5">
              <Globe className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              <h2
                id="site-status-heading"
                className="truncate text-sm font-semibold text-foreground"
              >
                {site.host}
              </h2>
            </div>
          </div>
          <div className="flex shrink-0 flex-wrap items-center gap-2">
            <Button variant="outline" size="sm" className="min-h-10 sm:min-h-8" asChild>
              <Link href="/seller/site">Chỉnh sửa site</Link>
            </Button>
            <Button size="sm" className="min-h-10 sm:min-h-8" asChild>
              <a href={site.url} target="_blank" rel="noopener noreferrer">
                <span>Xem site</span>
                <ExternalLink className="size-3.5" aria-hidden="true" />
              </a>
            </Button>
          </div>
        </CardContent>
      </Card>
    );
  }

  if (site.state === "suspended") {
    return (
      <Alert variant="destructive" aria-labelledby="site-status-heading">
        <AlertTriangle className="size-4" aria-hidden="true" />
        <AlertTitle className="flex flex-wrap items-center justify-between gap-2">
          <span className="flex flex-wrap items-center gap-2">
            <StatusBadge tone={siteStatusTone("SUSPENDED")}>Tạm khóa</StatusBadge>
            <span id="site-status-heading">{site.host || "Site gian hàng"}</span>
          </span>
          <Button variant="outline" size="xs" className="min-h-9 sm:min-h-7" asChild>
            <Link href="/seller/site">Xem tình trạng</Link>
          </Button>
        </AlertTitle>
        {site.suspendedReason ? <AlertDescription>{site.suspendedReason}</AlertDescription> : null}
      </Alert>
    );
  }

  const isDraft = site.state === "draft";
  return (
    <Alert variant="inverse" aria-labelledby="site-status-heading">
      <Sparkles className="size-4" aria-hidden="true" />
      <div className="col-start-2 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge tone={isDraft ? siteStatusTone("DRAFT") : "warning"}>
              {isDraft ? "Bản nháp" : "Chưa có site"}
            </StatusBadge>
            <h2
              id="site-status-heading"
              className="truncate text-sm font-semibold text-primary-foreground"
            >
              {isDraft ? site.host || "Site chưa xuất bản" : "Chưa mở bán công khai"}
            </h2>
          </div>
          <p className="text-[13px] text-topbar-foreground">
            Xuất bản site để nhận đơn hàng từ người mua.
          </p>
        </div>
        <Button
          size="sm"
          className="min-h-10 shrink-0 bg-card text-foreground shadow-none hover:bg-card/90 sm:min-h-8"
          asChild
        >
          <Link href="/seller/site">{isDraft ? "Xuất bản site" : "Thiết lập site"}</Link>
        </Button>
      </div>
    </Alert>
  );
}
