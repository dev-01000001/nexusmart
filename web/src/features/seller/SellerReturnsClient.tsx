"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { ExternalLink, Plus, RefreshCw, RotateCcw, Search, Trash2 } from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  Money,
  PageHeader,
  StatusBadge,
  Timeline,
  type IndexTableTab,
  type TimelineItem,
} from "@/components/app";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import type { ReturnSellerDecision } from "@/lib/gateway-types";
import {
  MAX_EVIDENCE_URLS,
  buildShippingLabelHref,
  getReturnFeeBearerLabel,
  getReturnStatusInfo,
  localizeTimelineNote,
  normalizeEnumSuffix,
  parseSafeEvidenceUrl,
  validateEvidenceUrls,
} from "@/features/account/orderMeta";
import { cn } from "@/lib/utils";
import { resolveSellerReturnTone } from "./ops/sellerTone";
import type { GwSellerReturnRequest } from "./sellerApiTypes";
import { useSellerContext } from "./SellerShell";
import { SHOP_PICKUP_ADDRESS_REQUIRED_SELLER_MESSAGE, parseUiState } from "./types";

type ReturnFilterKey = "all" | "PENDING" | "RETURNING" | "RECEIVED" | "ESCALATED" | "DONE";

const EMPTY_DASH = "—";

export interface SellerReturnsClientProps {
  initialReturns: GwSellerReturnRequest[];
  loadError: string | null;
}

export function SellerReturnsClient({ initialReturns, loadError }: SellerReturnsClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialSelectedParam = searchParams.get("selected");
  const initialStatusParam = (searchParams.get("status") ?? "").trim().toUpperCase();

  const { showToast } = useSellerContext();

  const [returns, setReturns] = useState<GwSellerReturnRequest[]>(initialReturns);
  const [refreshing, setRefreshing] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(loadError);
  const [filter, setFilter] = useState<ReturnFilterKey>(() => {
    if (["PENDING", "RETURNING", "RECEIVED", "ESCALATED", "DONE"].includes(initialStatusParam)) {
      return initialStatusParam as ReturnFilterKey;
    }
    return "all";
  });
  const [query, setQuery] = useState("");
  const [drawerReturnId, setDrawerReturnId] = useState<string | null>(initialSelectedParam);

  // Action form state inside Sheet
  const [responseNote, setResponseNote] = useState("");
  const [evidenceUrls, setEvidenceUrls] = useState<string[]>([""]);
  const [disputeReason, setDisputeReason] = useState("");
  const [disputeUrls, setDisputeUrls] = useState<string[]>([""]);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setReturns(initialReturns);
  }, [initialReturns]);

  const reloadReturns = useCallback(async () => {
    setRefreshing(true);
    setErrorMsg(null);
    try {
      const res = await api<{ items?: GwSellerReturnRequest[] }>(
        "/api/seller/returns?page=1&page_size=100"
      );
      setReturns(res.items ?? []);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách yêu cầu trả hàng lúc này."));
    } finally {
      setRefreshing(false);
    }
  }, []);

  const counts = useMemo(() => {
    const c = { all: returns.length, PENDING: 0, RETURNING: 0, RECEIVED: 0, ESCALATED: 0, DONE: 0 };
    for (const r of returns) {
      const st = normalizeEnumSuffix(r.status, "RETURN_STATUS_");
      if (st === "PENDING") c.PENDING += 1;
      else if (st === "AWAITING_SHIPMENT" || st === "IN_TRANSIT") c.RETURNING += 1;
      else if (st === "RECEIVED") c.RECEIVED += 1;
      else if (st === "ESCALATED" || st === "EVIDENCE_REQUESTED") c.ESCALATED += 1;
      else c.DONE += 1;
    }
    return c;
  }, [returns]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return returns.filter((r) => {
      const st = normalizeEnumSuffix(r.status, "RETURN_STATUS_");
      if (filter === "PENDING" && st !== "PENDING") return false;
      if (filter === "RETURNING" && st !== "AWAITING_SHIPMENT" && st !== "IN_TRANSIT") return false;
      if (filter === "RECEIVED" && st !== "RECEIVED") return false;
      if (filter === "ESCALATED" && st !== "ESCALATED" && st !== "EVIDENCE_REQUESTED") return false;
      if (
        filter === "DONE" &&
        st !== "REFUNDING" &&
        st !== "REFUNDED" &&
        st !== "REJECTED" &&
        st !== "CLOSED"
      ) {
        return false;
      }
      if (!q) return true;
      return (
        (r.order_code ?? "").toLowerCase().includes(q) ||
        (r.reason ?? "").toLowerCase().includes(q) ||
        (r.return_tracking_code ?? "").toLowerCase().includes(q)
      );
    });
  }, [returns, filter, query]);

  const activeReturn = useMemo(
    () => (drawerReturnId ? (returns.find((r) => r.id === drawerReturnId) ?? null) : null),
    [returns, drawerReturnId]
  );

  const openReturnDrawer = (ret: GwSellerReturnRequest) => {
    setDrawerReturnId(ret.id);
    setResponseNote("");
    setEvidenceUrls([""]);
    setDisputeReason("");
    setDisputeUrls([""]);
    setFormError(null);
  };

  const closeDrawer = () => {
    setDrawerReturnId(null);
    setFormError(null);
  };

  const handleRespond = async (ret: GwSellerReturnRequest, decision: ReturnSellerDecision) => {
    if (busy) return;
    const cleanNote = responseNote.trim();
    if (decision === "REJECT" && cleanNote.length < 5) {
      setFormError("Vui lòng nhập lý do từ chối tối thiểu 5 ký tự.");
      return;
    }
    const urlCheck = validateEvidenceUrls(evidenceUrls);
    if (!urlCheck.ok) {
      setFormError(urlCheck.error ?? "Liên kết bằng chứng chưa hợp lệ.");
      return;
    }
    setFormError(null);
    setBusy(true);
    try {
      const res = await api<{ return_request?: GwSellerReturnRequest }>(
        `/api/seller/returns/${encodeURIComponent(ret.id)}/respond`,
        {
          method: "POST",
          body: {
            decision,
            note: cleanNote,
            evidence_urls: urlCheck.urls,
          },
        }
      );
      if (res?.return_request) {
        setReturns((prev) => prev.map((item) => (item.id === ret.id ? res.return_request! : item)));
      } else {
        await reloadReturns();
      }
      setResponseNote("");
      setEvidenceUrls([""]);
      router.refresh();
      const actionMsg =
        decision === "ACCEPT_REFUND"
          ? "Đã chấp nhận hoàn tiền ngay không cần trả hàng."
          : decision === "ACCEPT_RETURN"
            ? "Đã chấp nhận trả hàng và tạo vận đơn chiều trả."
            : "Đã gửi phản hồi từ chối yêu cầu trả hàng.";
      showToast(actionMsg);
    } catch (err) {
      if (err instanceof ApiError && err.code === "SHOP_PICKUP_ADDRESS_REQUIRED") {
        setFormError(SHOP_PICKUP_ADDRESS_REQUIRED_SELLER_MESSAGE);
      } else {
        setFormError(getErrorMessage(err, "Không thể gửi phản hồi lúc này."));
      }
    } finally {
      setBusy(false);
    }
  };

  const handleConfirmReceived = async (ret: GwSellerReturnRequest) => {
    if (busy) return;
    setFormError(null);
    setBusy(true);
    try {
      const res = await api<{ return_request?: GwSellerReturnRequest }>(
        `/api/seller/returns/${encodeURIComponent(ret.id)}/confirm-received`,
        { method: "POST" }
      );
      if (res?.return_request) {
        setReturns((prev) => prev.map((item) => (item.id === ret.id ? res.return_request! : item)));
      } else {
        await reloadReturns();
      }
      router.refresh();
      showToast(`Đã xác nhận nhận hàng trả cho đơn ${ret.order_code}.`);
    } catch (err) {
      setFormError(getErrorMessage(err, "Không thể xác nhận đã nhận hàng trả lúc này."));
    } finally {
      setBusy(false);
    }
  };

  const handleDisputeGoods = async (ret: GwSellerReturnRequest) => {
    if (busy) return;
    const reason = disputeReason.trim();
    if (reason.length < 5) {
      setFormError("Vui lòng mô tả lý do hàng trả không đúng (tối thiểu 5 ký tự).");
      return;
    }
    const urlCheck = validateEvidenceUrls(disputeUrls);
    if (!urlCheck.ok) {
      setFormError(urlCheck.error ?? "Liên kết bằng chứng chưa hợp lệ.");
      return;
    }
    setFormError(null);
    setBusy(true);
    try {
      const res = await api<{ return_request?: GwSellerReturnRequest }>(
        `/api/seller/returns/${encodeURIComponent(ret.id)}/dispute`,
        {
          method: "POST",
          body: {
            reason,
            evidence_urls: urlCheck.urls,
          },
        }
      );
      if (res?.return_request) {
        setReturns((prev) => prev.map((item) => (item.id === ret.id ? res.return_request! : item)));
      } else {
        await reloadReturns();
      }
      setDisputeReason("");
      setDisputeUrls([""]);
      router.refresh();
      showToast(`Đã gửi báo cáo hàng sai cho đơn ${ret.order_code}.`);
    } catch (err) {
      setFormError(getErrorMessage(err, "Không thể gửi khiếu nại hàng trả lúc này."));
    } finally {
      setBusy(false);
    }
  };

  const returnTabs: IndexTableTab[] = [
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "PENDING", label: "Chờ phản hồi", count: counts.PENDING },
    { key: "RETURNING", label: "Đang gửi trả", count: counts.RETURNING },
    { key: "RECEIVED", label: "Chờ xác nhận nhận", count: counts.RECEIVED },
    { key: "ESCALATED", label: "Tranh chấp sàn", count: counts.ESCALATED },
    { key: "DONE", label: "Hoàn tiền / Đóng", count: counts.DONE },
  ];

  const activeStatusKey = activeReturn
    ? normalizeEnumSuffix(activeReturn.status, "RETURN_STATUS_")
    : "";

  const activeTimelineItems: TimelineItem[] = (activeReturn?.events ?? []).map((ev) => {
    const toInfo = getReturnStatusInfo(ev.to_status);
    const localized = localizeTimelineNote(ev.note);
    return {
      id: ev.id,
      at: ev.created_at ? formatDateTimeVi(ev.created_at) : EMPTY_DASH,
      title: toInfo.label,
      description: localized || undefined,
    };
  });

  return (
    <div className="space-y-4">
      <PageHeader
        title="Trả hàng & hoàn tiền"
        secondaryActions={
          <Button variant="secondary" size="sm" asChild>
            <Link href="/seller/don-hang">
              <span>Đơn hàng</span>
              <ExternalLink className="size-3.5" aria-hidden="true" />
            </Link>
          </Button>
        }
        primaryAction={
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={refreshing}
            onClick={() => void reloadReturns()}
          >
            <RefreshCw
              className={cn("size-3.5", refreshing && "animate-spin")}
              aria-hidden="true"
            />
            <span>{refreshing ? "Đang tải..." : "Làm mới"}</span>
          </Button>
        }
      />

      {uiState === "loading" ? (
        <Card role="status" aria-label="Đang tải danh sách yêu cầu trả hàng...">
          <CardContent className="space-y-3 py-5">
            {Array.from({ length: 5 }).map((_, idx) => (
              <Skeleton key={idx} className="h-12 w-full" />
            ))}
          </CardContent>
        </Card>
      ) : null}

      {uiState === "error" || (!uiState && errorMsg) ? (
        <ErrorState
          title="Không thể tải danh sách trả hàng"
          description={errorMsg ?? "Hệ thống xử lý trả hàng tạm gián đoạn."}
          onRetry={() => void reloadReturns()}
        />
      ) : null}

      {uiState === "empty" ? (
        <EmptyState
          icon={RotateCcw}
          title="Chưa có yêu cầu trả hàng nào"
          description="Khi người mua gửi yêu cầu trả hàng, hồ sơ sẽ hiển thị tại đây."
          action={
            <Button size="sm" asChild>
              <Link href="/seller/don-hang">Xem đơn hàng</Link>
            </Button>
          }
        />
      ) : null}

      {!uiState && !errorMsg ? (
        <IndexTable
          tabs={returnTabs}
          activeTab={filter}
          onTabChange={(nextKey) => setFilter(nextKey as ReturnFilterKey)}
          toolbar={
            <span className="text-xs text-muted-foreground tabular-nums">
              {formatInt(filtered.length)} / {formatInt(returns.length)} yêu cầu
            </span>
          }
        >
          <div className="border-b border-border bg-muted/40 px-3 py-2.5 md:px-4">
            <div className="relative max-w-sm">
              <Search
                className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground"
                aria-hidden="true"
              />
              <Input
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Tìm mã đơn, lý do, mã vận đơn trả..."
                aria-label="Tìm yêu cầu trả hàng"
                className="h-8 bg-card pl-8 text-[13px]"
              />
            </div>
          </div>

          {filtered.length === 0 ? (
            <div className="px-6 py-12 text-center text-sm text-muted-foreground">
              {returns.length === 0
                ? "Chưa có yêu cầu trả hàng nào."
                : "Không có yêu cầu trả hàng nào khớp bộ lọc hiện tại."}
            </div>
          ) : (
            <>
              {/* Desktop Table */}
              <div className="hidden md:block">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Mã đơn &amp; lý do</TableHead>
                      <TableHead className="text-right">Hoàn dự kiến</TableHead>
                      <TableHead>Vận đơn trả</TableHead>
                      <TableHead>Hạn xử lý</TableHead>
                      <TableHead>Trạng thái</TableHead>
                      <TableHead className="text-right">Thao tác</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {filtered.map((ret) => {
                      const meta = getReturnStatusInfo(ret.status);
                      const tone = resolveSellerReturnTone(ret.status);
                      const isSelected = drawerReturnId === ret.id;
                      return (
                        <TableRow
                          key={ret.id}
                          data-state={isSelected ? "selected" : undefined}
                          className="cursor-pointer"
                          onClick={() => openReturnDrawer(ret)}
                        >
                          <TableCell className="max-w-[280px]">
                            <button
                              type="button"
                              className="font-semibold text-foreground hover:text-brand hover:underline"
                              onClick={(e) => {
                                e.stopPropagation();
                                openReturnDrawer(ret);
                              }}
                            >
                              {ret.order_code || ret.order_id.slice(0, 8)}
                            </button>
                            <div className="truncate text-xs text-muted-foreground">
                              {ret.reason}
                            </div>
                          </TableCell>
                          <TableCell className="text-right">
                            <Money vnd={ret.refund_amount_vnd} />
                          </TableCell>
                          <TableCell className="font-mono text-xs text-foreground">
                            {ret.return_tracking_code || EMPTY_DASH}
                          </TableCell>
                          <TableCell className="text-xs text-muted-foreground tabular-nums">
                            {ret.deadline_at ? formatDateTimeVi(ret.deadline_at) : EMPTY_DASH}
                          </TableCell>
                          <TableCell>
                            <StatusBadge tone={tone}>{meta.label}</StatusBadge>
                          </TableCell>
                          <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                            <Button
                              type="button"
                              variant="outline"
                              size="xs"
                              onClick={() => openReturnDrawer(ret)}
                            >
                              Chi tiết
                            </Button>
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </div>

              {/* Mobile Card List */}
              <div className="md:hidden">
                {filtered.map((ret) => {
                  const meta = getReturnStatusInfo(ret.status);
                  const tone = resolveSellerReturnTone(ret.status);
                  return (
                    <div key={ret.id} className="border-b border-border last:border-b-0">
                      <IndexRowCard
                        title={ret.order_code || ret.order_id.slice(0, 8)}
                        badge={<StatusBadge tone={tone}>{meta.label}</StatusBadge>}
                        meta={
                          <>
                            <Money vnd={ret.refund_amount_vnd} size="sm" />
                            <span>·</span>
                            <span className="truncate">{ret.reason}</span>
                          </>
                        }
                        onClick={() => openReturnDrawer(ret)}
                        className="border-b-0 pb-2"
                      />
                      <div className="flex justify-end px-4 pb-3">
                        <Button
                          type="button"
                          variant="outline"
                          size="xs"
                          className="min-h-10"
                          onClick={() => openReturnDrawer(ret)}
                        >
                          Xử lý
                        </Button>
                      </div>
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </IndexTable>
      ) : null}

      {/* Detail & Actions Sheet */}
      <Sheet open={Boolean(activeReturn)} onOpenChange={(open) => !open && closeDrawer()}>
        <SheetContent side="right" className="w-full gap-0 p-0 sm:max-w-lg">
          {activeReturn ? (
            <>
              <SheetHeader className="border-b border-border px-5 py-4 pr-12">
                <div className="flex flex-wrap items-center gap-2">
                  <SheetTitle className="text-base font-semibold">
                    Yêu cầu trả hàng · {activeReturn.order_code}
                  </SheetTitle>
                  <StatusBadge tone={resolveSellerReturnTone(activeReturn.status)}>
                    {getReturnStatusInfo(activeReturn.status).label}
                  </StatusBadge>
                </div>
                <SheetDescription className="text-xs">
                  {getReturnStatusInfo(activeReturn.status).label}
                </SheetDescription>
              </SheetHeader>

              <div className="flex-1 space-y-5 overflow-y-auto px-5 py-4 text-sm">
                {/* Summary metadata */}
                <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <div>
                    <dt className="text-xs text-muted-foreground">Hoàn dự kiến</dt>
                    <dd className="mt-0.5">
                      <Money vnd={activeReturn.refund_amount_vnd} />
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted-foreground">Hạn xử lý kế tiếp</dt>
                    <dd className="mt-0.5 text-[13px] font-medium text-foreground tabular-nums">
                      {activeReturn.deadline_at
                        ? formatDateTimeVi(activeReturn.deadline_at)
                        : EMPTY_DASH}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted-foreground">Phí trả hàng</dt>
                    <dd className="mt-0.5 text-[13px] font-medium text-foreground tabular-nums">
                      {activeReturn.return_fee_vnd > 0
                        ? `${formatVnd(activeReturn.return_fee_vnd)} · `
                        : ""}
                      {getReturnFeeBearerLabel(activeReturn.return_fee_bearer)}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted-foreground">Vận đơn chiều trả</dt>
                    <dd className="mt-0.5 text-[13px] font-medium text-foreground">
                      {activeReturn.return_tracking_code ? (
                        <span className="inline-flex flex-wrap items-center gap-1.5">
                          <span className="font-mono text-xs">
                            {activeReturn.return_tracking_code}
                          </span>
                          {(() => {
                            const labelHref = buildShippingLabelHref(
                              activeReturn.return_tracking_code
                            );
                            if (!labelHref) return null;
                            return (
                              <a
                                href={labelHref}
                                target="_blank"
                                rel="noopener noreferrer"
                                className="text-xs font-medium text-brand hover:underline"
                                title="Mở nhãn trả hàng ở tab mới — nhấn Ctrl+P để in"
                              >
                                In nhãn trả hàng
                              </a>
                            );
                          })()}
                        </span>
                      ) : (
                        EMPTY_DASH
                      )}
                    </dd>
                  </div>
                </dl>

                <Separator />

                {/* Buyer & seller notes */}
                <div className="space-y-2.5 rounded-lg border border-border bg-muted/40 p-3.5 text-[13px]">
                  <h3 className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
                    Nội dung từ người mua
                  </h3>
                  <p className="text-foreground">{activeReturn.reason}</p>

                  {(() => {
                    const buyerLinks = (activeReturn.evidence_urls ?? [])
                      .map((u) => parseSafeEvidenceUrl(u))
                      .filter((x): x is NonNullable<typeof x> => x !== null);
                    if (buyerLinks.length === 0) return null;
                    return (
                      <div className="space-y-1 pt-1">
                        <div className="text-xs text-muted-foreground">Bằng chứng người mua</div>
                        {buyerLinks.map((ev, idx) => (
                          <a
                            key={`${ev.href}-${idx}`}
                            href={ev.href}
                            target="_blank"
                            rel="noopener noreferrer nofollow"
                            className="block break-all text-xs font-medium text-brand hover:underline"
                          >
                            Bằng chứng #{idx + 1} ({ev.hostname})
                          </a>
                        ))}
                      </div>
                    );
                  })()}

                  {activeReturn.seller_response || activeReturn.seller_note ? (
                    <p className="pt-1 text-foreground">
                      <strong>Phản hồi gian hàng:</strong>{" "}
                      {activeReturn.seller_response || activeReturn.seller_note}
                    </p>
                  ) : null}

                  {(() => {
                    const sellerLinks = (activeReturn.seller_evidence_urls ?? [])
                      .map((u) => parseSafeEvidenceUrl(u))
                      .filter((x): x is NonNullable<typeof x> => x !== null);
                    if (sellerLinks.length === 0) return null;
                    return (
                      <div className="space-y-1 pt-1">
                        <div className="text-xs text-muted-foreground">Chứng từ gian hàng</div>
                        {sellerLinks.map((ev, idx) => (
                          <a
                            key={`${ev.href}-${idx}`}
                            href={ev.href}
                            target="_blank"
                            rel="noopener noreferrer nofollow"
                            className="block break-all text-xs font-medium text-brand hover:underline"
                          >
                            Chứng từ #{idx + 1} ({ev.hostname})
                          </a>
                        ))}
                      </div>
                    );
                  })()}

                  {activeReturn.dispute_reason ? (
                    <p className="pt-1 text-foreground">
                      <strong>Nội dung tranh chấp:</strong> {activeReturn.dispute_reason}
                    </p>
                  ) : null}

                  {activeReturn.admin_note ? (
                    <p className="pt-1 text-foreground">
                      <strong>Kết luận quản trị viên:</strong> {activeReturn.admin_note}
                    </p>
                  ) : null}
                </div>

                {/* PENDING: ACCEPT_RETURN, ACCEPT_REFUND, or REJECT */}
                {activeStatusKey === "PENDING" ? (
                  <div className="space-y-3 rounded-lg border border-border bg-card p-3.5">
                    <h3 className="text-sm font-semibold text-foreground">Phản hồi yêu cầu</h3>
                    <Field>
                      <FieldLabel htmlFor="seller-ret-note">
                        Ghi chú phản hồi (bắt buộc nếu từ chối)
                      </FieldLabel>
                      <Textarea
                        id="seller-ret-note"
                        rows={3}
                        value={responseNote}
                        onChange={(e) => setResponseNote(e.target.value)}
                        placeholder="Nhập hướng dẫn gửi trả hoặc lý do từ chối..."
                        disabled={busy}
                      />
                    </Field>

                    <Field>
                      <FieldLabel>Liên kết chứng từ (tối đa {MAX_EVIDENCE_URLS})</FieldLabel>
                      <div className="space-y-2">
                        {evidenceUrls.map((url, idx) => (
                          <div key={`sel-ev-${idx}`} className="flex items-center gap-2">
                            <Input
                              type="url"
                              value={url}
                              onChange={(e) => {
                                const next = [...evidenceUrls];
                                next[idx] = e.target.value;
                                setEvidenceUrls(next);
                              }}
                              placeholder="https://..."
                              aria-label={`Liên kết chứng từ ${idx + 1}`}
                              disabled={busy}
                            />
                            {evidenceUrls.length > 1 ? (
                              <Button
                                type="button"
                                variant="outline"
                                size="icon-sm"
                                disabled={busy}
                                aria-label="Xóa liên kết chứng từ"
                                onClick={() =>
                                  setEvidenceUrls(evidenceUrls.filter((_, i) => i !== idx))
                                }
                              >
                                <Trash2 className="size-3.5" aria-hidden="true" />
                              </Button>
                            ) : null}
                          </div>
                        ))}
                        {evidenceUrls.length < MAX_EVIDENCE_URLS ? (
                          <Button
                            type="button"
                            variant="outline"
                            size="xs"
                            disabled={busy}
                            onClick={() => setEvidenceUrls([...evidenceUrls, ""])}
                          >
                            <Plus className="size-3.5" aria-hidden="true" />
                            <span>Thêm liên kết</span>
                          </Button>
                        ) : null}
                      </div>
                    </Field>

                    {formError ? <FieldError role="alert">{formError}</FieldError> : null}

                    <div className="flex flex-wrap justify-end gap-2 pt-1">
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={busy}
                        onClick={() => void handleRespond(activeReturn, "REJECT")}
                      >
                        Từ chối
                      </Button>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={busy}
                        onClick={() => void handleRespond(activeReturn, "ACCEPT_REFUND")}
                      >
                        Hoàn tiền ngay
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        disabled={busy}
                        onClick={() => void handleRespond(activeReturn, "ACCEPT_RETURN")}
                      >
                        {busy ? "Đang xử lý..." : "Yêu cầu trả hàng về"}
                      </Button>
                    </div>
                  </div>
                ) : null}

                {/* RECEIVED: Confirm received or Dispute */}
                {activeStatusKey === "RECEIVED" ? (
                  <div className="space-y-3 rounded-lg border border-border bg-card p-3.5">
                    <h3 className="text-sm font-semibold text-foreground">
                      Kiểm tra hàng trả về kho
                    </h3>
                    <Field>
                      <FieldLabel htmlFor="seller-dispute-reason">
                        Lý do báo hàng sai (chỉ nhập khi khiếu nại)
                      </FieldLabel>
                      <Textarea
                        id="seller-dispute-reason"
                        rows={3}
                        value={disputeReason}
                        onChange={(e) => setDisputeReason(e.target.value)}
                        placeholder="Mô tả sai khác giữa kiện hàng nhận về và đơn gốc..."
                        disabled={busy}
                      />
                    </Field>

                    <Field>
                      <FieldLabel>Bằng chứng mở kiện trả (tối đa {MAX_EVIDENCE_URLS})</FieldLabel>
                      <div className="space-y-2">
                        {disputeUrls.map((url, idx) => (
                          <div key={`sel-disp-${idx}`} className="flex items-center gap-2">
                            <Input
                              type="url"
                              value={url}
                              onChange={(e) => {
                                const next = [...disputeUrls];
                                next[idx] = e.target.value;
                                setDisputeUrls(next);
                              }}
                              placeholder="https://..."
                              aria-label={`Liên kết bằng chứng báo hàng sai ${idx + 1}`}
                              disabled={busy}
                            />
                            {disputeUrls.length > 1 ? (
                              <Button
                                type="button"
                                variant="outline"
                                size="icon-sm"
                                disabled={busy}
                                aria-label="Xóa liên kết bằng chứng"
                                onClick={() =>
                                  setDisputeUrls(disputeUrls.filter((_, i) => i !== idx))
                                }
                              >
                                <Trash2 className="size-3.5" aria-hidden="true" />
                              </Button>
                            ) : null}
                          </div>
                        ))}
                        {disputeUrls.length < MAX_EVIDENCE_URLS ? (
                          <Button
                            type="button"
                            variant="outline"
                            size="xs"
                            disabled={busy}
                            onClick={() => setDisputeUrls([...disputeUrls, ""])}
                          >
                            <Plus className="size-3.5" aria-hidden="true" />
                            <span>Thêm liên kết</span>
                          </Button>
                        ) : null}
                      </div>
                    </Field>

                    {formError ? <FieldError role="alert">{formError}</FieldError> : null}

                    <div className="flex flex-wrap justify-end gap-2 pt-1">
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={busy}
                        onClick={() => void handleDisputeGoods(activeReturn)}
                      >
                        Báo hàng sai
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        disabled={busy}
                        onClick={() => void handleConfirmReceived(activeReturn)}
                      >
                        {busy ? "Đang xử lý..." : "Xác nhận đã nhận đúng hàng"}
                      </Button>
                    </div>
                  </div>
                ) : null}

                {/* Timeline */}
                {activeTimelineItems.length > 0 ? (
                  <div className="space-y-2.5">
                    <h3 className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">
                      Lịch sử xử lý
                    </h3>
                    <Timeline items={activeTimelineItems} />
                  </div>
                ) : null}
              </div>

              <SheetFooter className="flex-row items-center justify-between gap-2 border-t border-border bg-card px-5 py-3.5">
                <Button variant="outline" size="sm" asChild>
                  <Link
                    href={`/seller/don-hang/${encodeURIComponent(activeReturn.order_id)}`}
                    onClick={closeDrawer}
                  >
                    <span>Mở chi tiết đơn</span>
                    <ExternalLink className="size-3.5" aria-hidden="true" />
                  </Link>
                </Button>
                <Button type="button" variant="ghost" size="sm" onClick={closeDrawer}>
                  Đóng
                </Button>
              </SheetFooter>
            </>
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  );
}
