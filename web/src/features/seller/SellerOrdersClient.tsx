"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  Check,
  Printer,
  RefreshCw,
  Search,
  ShoppingBag,
  SlidersHorizontal,
  Truck,
  X,
} from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  Money,
  PageHeader,
  StatusBadge,
  shipmentJobTone,
  type IndexTableTab,
  type StatusTone,
} from "@/components/app";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
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
import { api, apiBlob, getErrorMessage } from "@/lib/api";
import {
  GATEWAY_ORDER_STATUS_LABELS,
  formatCarrierLabel,
  formatDateTimeVi,
  formatInt,
  formatVnd,
} from "@/lib/format";
import {
  BATCH_POLL_MAX_OFF_PAGE_PER_TICK,
  BATCH_POLL_MAX_TICKS,
  MAX_BATCH_SHIP_ORDERS,
  SELLER_ORDERS_PAGE_SIZE,
  aggregateSellerOrderStatusCounts,
  buildSellerOrdersQuery,
  collectBatchTrackingCodes,
  resolveClampedPageOnEmpty,
  selectOffPageOrdersToPoll,
  toggleBatchSelection,
  translateBatchShipReasonCode,
  type BatchShipOrdersResponse,
  type PickingListResponse,
  type SellerOrderFilterKey,
  type SellerOrderStatusCount,
  type TrackedBatchOrderState,
} from "@/lib/a8-types";
import {
  getSalesChannelMeta,
  normalizeSalesChannel,
  parseSalesChannelFilter,
  type SalesChannelFilter,
} from "@/lib/salesChannel";
import { buildShippingLabelHref } from "@/features/account/orderMeta";
import { cn } from "@/lib/utils";
import { resolveSellerOrderTone } from "./ops/sellerTone";
import {
  CARRIER_OPTIONS,
  buildCarrierSelectOptions,
  canSellerConfirmOrder,
  canSellerShipOrder,
  parseUiState,
  resolveOrderShippingDefaults,
  translateSellerShipError,
} from "./types";
import {
  computeSellerNetReceivable,
  formatPaymentMethodVi,
  getNextBackendOrderAction,
  getSellerOrderStatusMeta,
  summarizeOrderItems,
  type GwSellerOrder,
} from "./sellerApiTypes";
import { SellerOrderQuickDrawer } from "./SellerOrderQuickDrawer";
import { useSellerContext } from "./SellerShell";

interface SelectedOrderMeta {
  order_code: string;
  tracking_code: string;
  status: string;
  shipment_job_status?: string;
}

export interface SellerOrdersClientProps {
  initialOrders: GwSellerOrder[];
  initialTotal: number;
  initialStatusCounts: SellerOrderStatusCount[];
  productImages: Record<string, string>;
  loadError: string | null;
}

function buildPaginationWindow(current: number, totalPages: number): number[] {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  }
  const pages = new Set<number>([1, totalPages, current]);
  for (let delta = -1; delta <= 1; delta += 1) {
    const p = current + delta;
    if (p > 1 && p < totalPages) pages.add(p);
  }
  return Array.from(pages).sort((a, b) => a - b);
}

function mapStatusParamToFilter(rawStatus: string | null): SellerOrderFilterKey {
  if (rawStatus === "CONFIRMED" || rawStatus === "cho_xac_nhan") return "CONFIRMED";
  if (rawStatus === "PREPARING" || rawStatus === "dang_chuan_bi") return "PREPARING";
  if (rawStatus === "SHIPPING" || rawStatus === "dang_giao") return "SHIPPING";
  if (rawStatus === "DONE" || rawStatus === "DELIVERED" || rawStatus === "COMPLETED") return "DONE";
  if (rawStatus === "OTHER" || rawStatus === "CANCELLED" || rawStatus === "tra_huy") return "OTHER";
  return "all";
}

const CHANNEL_ALL_VALUE = "__ALL__";
const CARRIER_KEEP_VALUE = "__KEEP__";

const CHANNEL_FILTER_OPTIONS: ReadonlyArray<{ value: SalesChannelFilter; label: string }> = [
  { value: "", label: "Mọi kênh bán" },
  { value: "MARKETPLACE", label: "Sàn NexusMart" },
  { value: "STOREFRONT", label: "Site riêng" },
];

export function SellerOrdersClient({
  initialOrders,
  initialTotal,
  initialStatusCounts,
  productImages,
  loadError,
}: SellerOrdersClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialFilter = mapStatusParamToFilter(searchParams.get("status"));
  const initialChannel: SalesChannelFilter =
    parseSalesChannelFilter(searchParams.get("sales_channel")) ?? "";

  const { showToast } = useSellerContext();

  const [orders, setOrders] = useState<GwSellerOrder[]>(initialOrders);
  const [total, setTotal] = useState(initialTotal);
  const [statusCounts, setStatusCounts] = useState<SellerOrderStatusCount[]>(initialStatusCounts);
  const [loading, setLoading] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(loadError);

  // Server-side filters & pagination
  const [page, setPage] = useState(1);
  const [filter, setFilter] = useState<SellerOrderFilterKey>(initialFilter);
  const [queryInput, setQueryInput] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [carrierFilter, setCarrierFilter] = useState("all");
  const [paymentFilter, setPaymentFilter] = useState<"all" | "COD" | "VIETQR" | "ZALOPAY">("all");
  const [channelFilter, setChannelFilter] = useState<SalesChannelFilter>(initialChannel);
  const [dueBeforeDate, setDueBeforeDate] = useState("");
  const [filtersOpen, setFiltersOpen] = useState(true);

  const [busyOrderId, setBusyOrderId] = useState<string | null>(null);
  const [confirmBatchBusy, setConfirmBatchBusy] = useState(false);

  // Quick slide-over drawer + inline single-order ship form
  const [drawerOrderId, setDrawerOrderId] = useState<string | null>(null);
  const [shippingOrderId, setShippingOrderId] = useState<string | null>(null);
  const [carrierCode, setCarrierCode] = useState(CARRIER_OPTIONS[0].carrierCode);
  const [serviceCode, setServiceCode] = useState(CARRIER_OPTIONS[0].services[0].serviceCode);

  // Wave 3b-3 batch ship, picking list & merged PDF labels
  const [selectedOrderIds, setSelectedOrderIds] = useState<string[]>([]);
  const [selectedMetaById, setSelectedMetaById] = useState<Record<string, SelectedOrderMeta>>({});
  const [batchCarrierCode, setBatchCarrierCode] = useState("");
  const [batchServiceCode, setBatchServiceCode] = useState("");
  const [batchShipBusy, setBatchShipBusy] = useState(false);
  const [batchShipResult, setBatchShipResult] = useState<BatchShipOrdersResponse | null>(null);
  const [batchTrackedById, setBatchTrackedById] = useState<Record<string, TrackedBatchOrderState>>(
    {}
  );
  const [pollTickVersion, setPollTickVersion] = useState(0);
  const [pickingBusy, setPickingBusy] = useState(false);
  const [pickingList, setPickingList] = useState<PickingListResponse | null>(null);
  const [pdfBusy, setPdfBusy] = useState(false);

  const loadSeqRef = useRef(0);
  const pollCountRef = useRef(0);
  const offPagePollCursorRef = useRef(0);
  const didSkipInitialFetchRef = useRef(false);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedQuery(queryInput.trim());
      setPage(1);
    }, 250);
    return () => window.clearTimeout(timer);
  }, [queryInput]);

  const loadOrders = useCallback(
    async (opts?: { silent?: boolean }) => {
      const seq = ++loadSeqRef.current;
      if (!opts?.silent) {
        setLoading(true);
        setErrorMsg(null);
      }
      try {
        const baseQs = buildSellerOrdersQuery({
          page,
          pageSize: SELLER_ORDERS_PAGE_SIZE,
          filter,
          carrierCode: carrierFilter,
          paymentMethod: paymentFilter,
          keyword: debouncedQuery,
          dueBeforeDate,
          includeStatusCounts: true,
        });
        const params = new URLSearchParams(baseQs);
        if (channelFilter) {
          params.set("sales_channel", channelFilter);
        }
        const res = await api<{
          items?: GwSellerOrder[];
          total?: number;
          status_counts?: SellerOrderStatusCount[];
        }>(`/api/seller/orders?${params.toString()}`);

        if (loadSeqRef.current !== seq) return;
        const items = Array.isArray(res?.items) ? res.items : [];
        const resolvedTotal = typeof res?.total === "number" ? res.total : items.length;
        if (Array.isArray(res?.status_counts)) {
          setStatusCounts(res.status_counts);
        }
        const clampedPage = resolveClampedPageOnEmpty(
          page,
          items.length,
          resolvedTotal,
          SELLER_ORDERS_PAGE_SIZE
        );
        if (clampedPage !== null) {
          setTotal(resolvedTotal);
          setPage(clampedPage);
          return;
        }
        setOrders(items);
        setTotal(resolvedTotal);
        setSelectedMetaById((prev) => {
          const next = { ...prev };
          for (const ord of items) {
            if (ord.id in next) {
              next[ord.id] = {
                order_code: ord.order_code,
                tracking_code: ord.tracking_code ?? "",
                status: ord.status,
                shipment_job_status: ord.shipment_job_status,
              };
            }
          }
          return next;
        });
        setBatchTrackedById((prev) => {
          let changed = false;
          const next = { ...prev };
          for (const ord of items) {
            if (ord.id in next) {
              next[ord.id] = {
                tracking_code: ord.tracking_code ?? "",
                status: ord.status,
                shipment_job_status: ord.shipment_job_status ?? "",
              };
              changed = true;
            }
          }
          return changed ? next : prev;
        });
      } catch (err) {
        if (loadSeqRef.current !== seq) return;
        if (!opts?.silent) {
          setErrorMsg(getErrorMessage(err, "Không thể tải danh sách đơn hàng lúc này."));
        }
      } finally {
        if (loadSeqRef.current === seq && !opts?.silent) {
          setLoading(false);
        }
      }
    },
    [page, filter, carrierFilter, paymentFilter, channelFilter, debouncedQuery, dueBeforeDate]
  );

  useEffect(() => {
    if (uiState) return;
    const isDefaultUnfiltered =
      page === 1 &&
      filter === "all" &&
      carrierFilter === "all" &&
      paymentFilter === "all" &&
      channelFilter === "" &&
      !debouncedQuery &&
      !dueBeforeDate;
    if (!didSkipInitialFetchRef.current && isDefaultUnfiltered && !loadError) {
      didSkipInitialFetchRef.current = true;
      return;
    }
    didSkipInitialFetchRef.current = true;
    void loadOrders();
  }, [
    uiState,
    loadOrders,
    page,
    filter,
    carrierFilter,
    paymentFilter,
    channelFilter,
    debouncedQuery,
    dueBeforeDate,
    loadError,
  ]);

  const ordersById = useMemo(() => {
    const map = new Map<string, GwSellerOrder>();
    for (const o of orders) map.set(o.id, o);
    return map;
  }, [orders]);

  const hasOnPageQueuedOrder = useMemo(
    () =>
      orders.some(
        (o) => !o.tracking_code && (o.shipment_job_status ?? "").trim().toUpperCase() === "QUEUED"
      ),
    [orders]
  );

  const offPagePendingCount = useMemo(() => {
    if (!batchShipResult) return 0;
    return selectOffPageOrdersToPoll(
      batchShipResult.results ?? [],
      ordersById,
      batchTrackedById,
      0,
      BATCH_POLL_MAX_OFF_PAGE_PER_TICK
    ).pendingCount;
  }, [batchShipResult, ordersById, batchTrackedById]);

  const hasQueuedShipmentJob = hasOnPageQueuedOrder || offPagePendingCount > 0;

  const pollBatchProgress = useCallback(async () => {
    const plan = batchShipResult
      ? selectOffPageOrdersToPoll(
          batchShipResult.results ?? [],
          ordersById,
          batchTrackedById,
          offPagePollCursorRef.current,
          BATCH_POLL_MAX_OFF_PAGE_PER_TICK
        )
      : { ids: [], pendingCount: 0, nextCursor: 0 };
    offPagePollCursorRef.current = plan.nextCursor;

    const tasks: Array<Promise<unknown>> = [loadOrders({ silent: true })];
    if (plan.ids.length > 0) {
      tasks.push(
        Promise.allSettled(
          plan.ids.map((id) =>
            api<{ order: GwSellerOrder }>(`/api/seller/orders/${encodeURIComponent(id)}`)
          )
        ).then((results) => {
          const fetched: GwSellerOrder[] = [];
          for (const r of results) {
            if (r.status === "fulfilled" && r.value?.order) {
              fetched.push(r.value.order);
            }
          }
          if (fetched.length === 0) return;
          setBatchTrackedById((prev) => {
            const next = { ...prev };
            for (const ord of fetched) {
              next[ord.id] = {
                tracking_code: ord.tracking_code ?? "",
                status: ord.status,
                shipment_job_status: ord.shipment_job_status ?? "",
              };
            }
            return next;
          });
          setSelectedMetaById((prev) => {
            let changed = false;
            const next = { ...prev };
            for (const ord of fetched) {
              if (ord.id in next) {
                next[ord.id] = {
                  order_code: ord.order_code,
                  tracking_code: ord.tracking_code ?? "",
                  status: ord.status,
                  shipment_job_status: ord.shipment_job_status,
                };
                changed = true;
              }
            }
            return changed ? next : prev;
          });
        })
      );
    }

    await Promise.all(tasks);
    setPollTickVersion((v) => v + 1);
  }, [batchShipResult, ordersById, batchTrackedById, loadOrders]);

  useEffect(() => {
    if (uiState || !hasQueuedShipmentJob) {
      pollCountRef.current = 0;
      return;
    }
    if (pollCountRef.current >= BATCH_POLL_MAX_TICKS) return;
    const timer = window.setTimeout(() => {
      pollCountRef.current += 1;
      void pollBatchProgress();
    }, 3000);
    return () => window.clearTimeout(timer);
  }, [uiState, hasQueuedShipmentJob, pollBatchProgress, pollTickVersion]);

  const counts = useMemo(
    () => aggregateSellerOrderStatusCounts(statusCounts, total),
    [statusCounts, total]
  );

  const totalPages = Math.max(1, Math.ceil(total / SELLER_ORDERS_PAGE_SIZE));
  const startIndex = total === 0 ? 0 : (page - 1) * SELLER_ORDERS_PAGE_SIZE + 1;
  const endIndex = Math.min(total, (page - 1) * SELLER_ORDERS_PAGE_SIZE + orders.length);

  const pageOrderIds = useMemo(() => orders.map((o) => o.id), [orders]);
  const selectedSet = useMemo(() => new Set(selectedOrderIds), [selectedOrderIds]);
  const allPageSelected =
    pageOrderIds.length > 0 && pageOrderIds.every((id) => selectedSet.has(id));

  const selectedTrackingCodes = useMemo(() => {
    const entries = selectedOrderIds.map((id) => {
      const live = ordersById.get(id);
      const tracked = batchTrackedById[id];
      const cached = selectedMetaById[id];
      return {
        tracking_code: live?.tracking_code || tracked?.tracking_code || cached?.tracking_code || "",
      };
    });
    return collectBatchTrackingCodes(entries, MAX_BATCH_SHIP_ORDERS);
  }, [selectedOrderIds, ordersById, batchTrackedById, selectedMetaById]);

  const handleToggleSelectOrder = (order: GwSellerOrder) => {
    const { next, capped } = toggleBatchSelection(
      selectedOrderIds,
      order.id,
      MAX_BATCH_SHIP_ORDERS
    );
    if (capped) {
      showToast(`Chỉ được chọn tối đa ${MAX_BATCH_SHIP_ORDERS} đơn hàng mỗi lô.`);
      return;
    }
    setSelectedOrderIds(next);
    setSelectedMetaById((prev) => {
      const copy = { ...prev };
      if (next.includes(order.id)) {
        copy[order.id] = {
          order_code: order.order_code,
          tracking_code: order.tracking_code ?? "",
          status: order.status,
          shipment_job_status: order.shipment_job_status,
        };
      } else {
        delete copy[order.id];
      }
      return copy;
    });
  };

  const handleToggleSelectPage = () => {
    if (allPageSelected) {
      const pageSet = new Set(pageOrderIds);
      setSelectedOrderIds((prev) => prev.filter((id) => !pageSet.has(id)));
      setSelectedMetaById((prev) => {
        const copy = { ...prev };
        for (const id of pageOrderIds) delete copy[id];
        return copy;
      });
      return;
    }

    const nextIds = [...selectedOrderIds];
    const nextMeta = { ...selectedMetaById };
    let hitCap = false;
    for (const ord of orders) {
      if (nextIds.includes(ord.id)) continue;
      if (nextIds.length >= MAX_BATCH_SHIP_ORDERS) {
        hitCap = true;
        break;
      }
      nextIds.push(ord.id);
      nextMeta[ord.id] = {
        order_code: ord.order_code,
        tracking_code: ord.tracking_code ?? "",
        status: ord.status,
        shipment_job_status: ord.shipment_job_status,
      };
    }
    setSelectedOrderIds(nextIds);
    setSelectedMetaById(nextMeta);
    if (hitCap) {
      showToast(`Đã đạt giới hạn tối đa ${MAX_BATCH_SHIP_ORDERS} đơn hàng mỗi lô.`);
    }
  };

  const handleClearSelection = () => {
    setSelectedOrderIds([]);
    setSelectedMetaById({});
  };

  const shippingOrder = useMemo(
    () => orders.find((o) => o.id === shippingOrderId) ?? null,
    [orders, shippingOrderId]
  );

  const carrierOptions = useMemo(() => buildCarrierSelectOptions(shippingOrder), [shippingOrder]);
  const activeCarrierOption = useMemo(
    () => carrierOptions.find((c) => c.carrierCode === carrierCode) ?? carrierOptions[0],
    [carrierOptions, carrierCode]
  );
  const activeBatchCarrier = useMemo(
    () => CARRIER_OPTIONS.find((c) => c.carrierCode === batchCarrierCode) ?? null,
    [batchCarrierCode]
  );

  const handleToggleShipDrawer = (order: GwSellerOrder) => {
    if (shippingOrderId === order.id) {
      setShippingOrderId(null);
      return;
    }
    const defaults = resolveOrderShippingDefaults(order);
    setCarrierCode(defaults.carrierCode);
    setServiceCode(defaults.serviceCode);
    setShippingOrderId(order.id);
  };

  const handleCarrierChange = (nextCarrier: string) => {
    setCarrierCode(nextCarrier);
    const opt = carrierOptions.find((c) => c.carrierCode === nextCarrier);
    if (opt && opt.services[0]) {
      setServiceCode(opt.services[0].serviceCode);
    }
  };

  const handleConfirmOrder = async (order: GwSellerOrder) => {
    if (!canSellerConfirmOrder(order.status, order.shipment_job_status) || busyOrderId) return;
    setBusyOrderId(order.id);
    try {
      await api<{ order: GwSellerOrder }>(
        `/api/seller/orders/${encodeURIComponent(order.id)}/confirm`,
        { method: "POST" }
      );
      await loadOrders();
      router.refresh();
      showToast(`Đã xác nhận đơn ${order.order_code}.`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xác nhận đơn hàng lúc này."));
    } finally {
      setBusyOrderId(null);
    }
  };

  const handleShipOrder = async (
    order: GwSellerOrder,
    override?: { carrier_code: string; service_code: string }
  ) => {
    if (!canSellerShipOrder(order.status, order.tracking_code) || busyOrderId) return;
    setBusyOrderId(order.id);
    try {
      const res = await api<{
        order: GwSellerOrder;
        tracking_code?: string;
        label_url?: string;
        queued?: boolean;
        shipment_job_id?: string;
      }>(`/api/seller/orders/${encodeURIComponent(order.id)}/ship`, {
        method: "POST",
        body: {
          carrier_code: override?.carrier_code ?? carrierCode,
          service_code: override?.service_code ?? serviceCode,
        },
      });
      setShippingOrderId(null);
      await loadOrders();
      router.refresh();
      if (res.queued) {
        showToast(
          `Đơn ${order.order_code} đang chờ tạo vận đơn với hãng — bạn có thể bấm Thử tạo lại ngay.`
        );
      } else {
        showToast(
          res.tracking_code
            ? `Đã tạo vận đơn ${res.tracking_code} cho đơn ${order.order_code}.`
            : `Đã tạo vận đơn cho đơn ${order.order_code}.`
        );
      }
    } catch (err) {
      showToast(translateSellerShipError(err));
    } finally {
      setBusyOrderId(null);
    }
  };

  const handleConfirmPagePending = async () => {
    const pending = orders.filter((o) => canSellerConfirmOrder(o.status, o.shipment_job_status));
    if (pending.length === 0 || confirmBatchBusy) return;
    setConfirmBatchBusy(true);
    let okCount = 0;
    try {
      for (const order of pending) {
        try {
          await api<{ order: GwSellerOrder }>(
            `/api/seller/orders/${encodeURIComponent(order.id)}/confirm`,
            { method: "POST" }
          );
          okCount += 1;
        } catch {
          // Continue confirming remaining orders
        }
      }
      await loadOrders();
      router.refresh();
      showToast(`Đã xác nhận ${formatInt(okCount)}/${formatInt(pending.length)} đơn chờ.`);
    } finally {
      setConfirmBatchBusy(false);
    }
  };

  const handleBatchShip = async () => {
    if (selectedOrderIds.length === 0 || batchShipBusy) return;
    setBatchShipBusy(true);
    try {
      const res = await api<BatchShipOrdersResponse>("/api/seller/orders/batch-ship", {
        method: "POST",
        body: {
          order_ids: selectedOrderIds.slice(0, MAX_BATCH_SHIP_ORDERS),
          carrier_code: batchCarrierCode,
          service_code: batchServiceCode,
        },
      });
      setBatchShipResult(res);
      setBatchTrackedById(() => {
        const next: Record<string, TrackedBatchOrderState> = {};
        for (const row of res.results ?? []) {
          if (row.outcome === "QUEUED" || row.tracking_code) {
            next[row.order_id] = {
              tracking_code: row.tracking_code ?? "",
              status: "",
              shipment_job_status: row.outcome === "QUEUED" ? "QUEUED" : "",
            };
          }
        }
        return next;
      });
      pollCountRef.current = 0;
      offPagePollCursorRef.current = 0;
      await loadOrders();
      router.refresh();
      showToast(
        `Đã xếp hàng tạo vận đơn cho ${formatInt(res.queued_count)} đơn (${formatInt(res.skipped_count)} bỏ qua).`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tạo vận đơn hàng loạt lúc này."));
    } finally {
      setBatchShipBusy(false);
    }
  };

  const handleLoadPickingList = async () => {
    if (selectedOrderIds.length === 0 || pickingBusy) return;
    setPickingBusy(true);
    try {
      const res = await api<PickingListResponse>("/api/seller/orders/picking-list", {
        method: "POST",
        body: { order_ids: selectedOrderIds.slice(0, MAX_BATCH_SHIP_ORDERS) },
      });
      setPickingList(res);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tạo danh sách nhặt hàng lúc này."));
    } finally {
      setPickingBusy(false);
    }
  };

  const handleBatchLabelsPdf = async () => {
    if (pdfBusy) return;
    if (selectedTrackingCodes.length === 0) {
      showToast("Các đơn đã chọn chưa có mã vận đơn.");
      return;
    }
    setPdfBusy(true);
    try {
      const blob = await apiBlob("/api/shipping/labels/batch", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/pdf",
        },
        body: JSON.stringify({ tracking_codes: selectedTrackingCodes }),
      });
      const pdfBlob =
        blob.type === "application/pdf" ? blob : new Blob([blob], { type: "application/pdf" });
      const url = URL.createObjectURL(pdfBlob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `nhan-van-don-gop-${selectedTrackingCodes.length}.pdf`;
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
      showToast(`Đã tải nhãn gộp PDF cho ${formatInt(selectedTrackingCodes.length)} vận đơn.`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể tải nhãn vận đơn gộp PDF lúc này."));
    } finally {
      setPdfBusy(false);
    }
  };

  const hasActiveFilters =
    filter !== "all" ||
    Boolean(queryInput.trim()) ||
    carrierFilter !== "all" ||
    paymentFilter !== "all" ||
    channelFilter !== "" ||
    Boolean(dueBeforeDate);

  const handleResetFilters = () => {
    setFilter("all");
    setQueryInput("");
    setDebouncedQuery("");
    setCarrierFilter("all");
    setPaymentFilter("all");
    setChannelFilter("");
    setDueBeforeDate("");
    setPage(1);
  };

  const orderTabs: IndexTableTab[] = [
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "CONFIRMED", label: GATEWAY_ORDER_STATUS_LABELS.CONFIRMED, count: counts.CONFIRMED },
    { key: "PREPARING", label: GATEWAY_ORDER_STATUS_LABELS.PREPARING, count: counts.PREPARING },
    { key: "SHIPPING", label: GATEWAY_ORDER_STATUS_LABELS.SHIPPING, count: counts.SHIPPING },
    { key: "DONE", label: "Đã giao / Hoàn tất", count: counts.DONE },
    { key: "OTHER", label: "Chờ TT / Hủy / Trả", count: counts.OTHER },
  ];

  const pageConfirmedCount = orders.filter((o) =>
    canSellerConfirmOrder(o.status, o.shipment_job_status)
  ).length;
  const pageButtons = buildPaginationWindow(page, totalPages);
  const drawerOrder = useMemo(
    () => (drawerOrderId ? (orders.find((o) => o.id === drawerOrderId) ?? null) : null),
    [orders, drawerOrderId]
  );

  return (
    <div className="space-y-4">
      <PageHeader
        title="Đơn hàng"
        secondaryActions={
          <Button
            type="button"
            variant="secondary"
            size="sm"
            disabled={loading}
            onClick={() => void loadOrders()}
          >
            <RefreshCw className={cn("size-3.5", loading && "animate-spin")} aria-hidden="true" />
            <span>Làm mới</span>
          </Button>
        }
        primaryAction={
          pageConfirmedCount > 0 ? (
            <Button
              type="button"
              size="sm"
              disabled={confirmBatchBusy}
              onClick={() => void handleConfirmPagePending()}
            >
              <Check className="size-4" aria-hidden="true" />
              <span>
                {confirmBatchBusy
                  ? "Đang xác nhận..."
                  : `Xác nhận chờ (${formatInt(pageConfirmedCount)})`}
              </span>
            </Button>
          ) : null
        }
      />

      {uiState === "loading" || (!uiState && loading && orders.length === 0) ? (
        <Card role="status" aria-label="Đang tải danh sách đơn hàng...">
          <CardContent className="space-y-3 py-5">
            <Skeleton className="h-8 w-full" />
            {Array.from({ length: 5 }).map((_, idx) => (
              <Skeleton key={idx} className="h-12 w-full" />
            ))}
          </CardContent>
        </Card>
      ) : null}

      {uiState === "error" || (!uiState && !loading && errorMsg) ? (
        <ErrorState
          title="Không thể tải danh sách đơn hàng"
          description={errorMsg ?? "Kết nối đến hệ thống xử lý đơn hàng tạm gián đoạn."}
          onRetry={() => void loadOrders()}
        />
      ) : null}

      {uiState === "empty" ? (
        <EmptyState
          icon={ShoppingBag}
          title="Chưa có đơn hàng nào"
          description="Khi người mua đặt hàng từ gian hàng, đơn sẽ xuất hiện tại đây."
          action={
            <Button size="sm" asChild>
              <Link href="/seller/san-pham">Xem sản phẩm</Link>
            </Button>
          }
        />
      ) : null}

      {!uiState && !(loading && orders.length === 0) && !errorMsg ? (
        <>
          {/* Batch Ship Result Card */}
          {batchShipResult ? (
            <Card aria-label="Kết quả tạo vận đơn hàng loạt">
              <CardHeader>
                <CardTitle className="tabular-nums">
                  Kết quả lô vận đơn: {formatInt(batchShipResult.queued_count)} đã xếp hàng ·{" "}
                  {formatInt(batchShipResult.skipped_count)} bỏ qua
                </CardTitle>
                <CardAction>
                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      onClick={() => {
                        pollCountRef.current = 0;
                        void pollBatchProgress();
                      }}
                    >
                      Cập nhật tiến độ
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="xs"
                      onClick={() => {
                        setBatchShipResult(null);
                        setBatchTrackedById({});
                      }}
                    >
                      Đóng
                    </Button>
                  </div>
                </CardAction>
              </CardHeader>
              <CardContent className="px-0 pb-0">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Mã đơn</TableHead>
                      <TableHead>Kết quả lô</TableHead>
                      <TableHead>Trạng thái xử lý</TableHead>
                      <TableHead>Chi tiết</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(batchShipResult.results ?? []).map((item) => {
                      const liveOrder = ordersById.get(item.order_id);
                      const trackedOrder = batchTrackedById[item.order_id];
                      const liveTracking =
                        liveOrder?.tracking_code ||
                        trackedOrder?.tracking_code ||
                        item.tracking_code;
                      const liveJobStatus = (
                        liveOrder?.shipment_job_status ||
                        trackedOrder?.shipment_job_status ||
                        ""
                      ).toUpperCase();
                      const isQueuedOutcome = item.outcome === "QUEUED";

                      let progressLabel = "Bỏ qua";
                      let progressTone: StatusTone = "neutral";
                      if (liveTracking) {
                        progressLabel = `Đã có mã: ${liveTracking}`;
                        progressTone = "success";
                      } else if (liveJobStatus === "FAILED") {
                        progressLabel = "Tạo vận đơn thất bại";
                        progressTone = shipmentJobTone("FAILED");
                      } else if (isQueuedOutcome || liveJobStatus === "QUEUED") {
                        progressLabel = "Đang chờ tạo vận đơn";
                        progressTone = shipmentJobTone("QUEUED");
                      }

                      const detailText = isQueuedOutcome
                        ? liveTracking
                          ? "Đã tạo vận đơn thành công."
                          : liveJobStatus === "FAILED"
                            ? "Tạo vận đơn thất bại. Mở chi tiết đơn để thử lại."
                            : "Đã đưa vào hàng đợi tạo vận đơn với hãng."
                        : translateBatchShipReasonCode(item.reason_code);

                      return (
                        <TableRow key={item.order_id}>
                          <TableCell>
                            <Link
                              href={`/seller/don-hang/${item.order_id}`}
                              className="font-semibold text-foreground hover:text-brand hover:underline"
                            >
                              {item.order_code || item.order_id.slice(0, 8)}
                            </Link>
                          </TableCell>
                          <TableCell>
                            <StatusBadge tone={isQueuedOutcome ? "info" : "critical"}>
                              {isQueuedOutcome ? "Đã xếp hàng" : "Bỏ qua"}
                            </StatusBadge>
                          </TableCell>
                          <TableCell>
                            <StatusBadge tone={progressTone}>{progressLabel}</StatusBadge>
                          </TableCell>
                          <TableCell className="whitespace-normal text-[13px] text-muted-foreground">
                            {detailText}
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>
          ) : null}

          <IndexTable
            tabs={orderTabs}
            activeTab={filter}
            onTabChange={(nextKey) => {
              setFilter(nextKey as SellerOrderFilterKey);
              setPage(1);
            }}
            toolbar={
              <div className="flex items-center gap-2">
                <span className="hidden text-xs text-muted-foreground tabular-nums sm:inline">
                  {loading
                    ? "Đang tải..."
                    : `Hiển thị ${formatInt(startIndex)}–${formatInt(endIndex)} / ${formatInt(total)}`}
                </span>
                <Button
                  type="button"
                  variant={filtersOpen ? "secondary" : "outline"}
                  size="icon-sm"
                  aria-label="Ẩn hoặc hiện bộ lọc đơn hàng"
                  aria-expanded={filtersOpen}
                  onClick={() => setFiltersOpen((v) => !v)}
                >
                  <SlidersHorizontal className="size-4" aria-hidden="true" />
                </Button>
              </div>
            }
            selection={
              selectedOrderIds.length > 0
                ? {
                    count: selectedOrderIds.length,
                    onClear: handleClearSelection,
                    actions: (
                      <>
                        <span className="text-xs text-muted-foreground tabular-nums">
                          ({formatInt(selectedTrackingCodes.length)}/{MAX_BATCH_SHIP_ORDERS} đã có
                          vận đơn)
                        </span>
                        <Select
                          value={batchCarrierCode || CARRIER_KEEP_VALUE}
                          onValueChange={(val) => {
                            const nextCarrier = val === CARRIER_KEEP_VALUE ? "" : val;
                            setBatchCarrierCode(nextCarrier);
                            const opt = CARRIER_OPTIONS.find((c) => c.carrierCode === nextCarrier);
                            setBatchServiceCode(opt?.services[0]?.serviceCode ?? "");
                          }}
                        >
                          <SelectTrigger
                            aria-label="Hãng vận chuyển hàng loạt"
                            className="h-8 w-auto min-w-[160px] bg-card text-xs"
                          >
                            <SelectValue>
                              {batchCarrierCode
                                ? (CARRIER_OPTIONS.find((c) => c.carrierCode === batchCarrierCode)
                                    ?.carrierLabel ?? batchCarrierCode)
                                : "Giữ hãng theo từng đơn"}
                            </SelectValue>
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value={CARRIER_KEEP_VALUE}>
                              Giữ hãng theo từng đơn
                            </SelectItem>
                            {CARRIER_OPTIONS.map((c) => (
                              <SelectItem key={c.carrierCode} value={c.carrierCode}>
                                {c.carrierLabel}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                        {activeBatchCarrier ? (
                          <Select value={batchServiceCode} onValueChange={setBatchServiceCode}>
                            <SelectTrigger
                              aria-label="Gói dịch vụ vận chuyển hàng loạt"
                              className="h-8 w-auto min-w-[140px] bg-card text-xs"
                            >
                              <SelectValue>
                                {activeBatchCarrier.services.find(
                                  (s) => s.serviceCode === batchServiceCode
                                )?.serviceLabel ?? batchServiceCode}
                              </SelectValue>
                            </SelectTrigger>
                            <SelectContent>
                              {activeBatchCarrier.services.map((s) => (
                                <SelectItem key={s.serviceCode} value={s.serviceCode}>
                                  {s.serviceLabel}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        ) : null}
                        <Button
                          type="button"
                          size="xs"
                          disabled={batchShipBusy}
                          onClick={() => void handleBatchShip()}
                        >
                          <Truck className="size-3.5" aria-hidden="true" />
                          <span>
                            {batchShipBusy ? "Đang xếp hàng..." : "Tạo vận đơn hàng loạt"}
                          </span>
                        </Button>
                        <Button
                          type="button"
                          variant="outline"
                          size="xs"
                          disabled={pickingBusy}
                          onClick={() => void handleLoadPickingList()}
                        >
                          {pickingBusy ? "Đang tổng hợp..." : "In danh sách nhặt hàng"}
                        </Button>
                        <Button
                          type="button"
                          variant="outline"
                          size="xs"
                          disabled={pdfBusy || selectedTrackingCodes.length === 0}
                          onClick={() => void handleBatchLabelsPdf()}
                        >
                          <Printer className="size-3.5" aria-hidden="true" />
                          <span>
                            {pdfBusy
                              ? "Đang xuất PDF..."
                              : `In nhãn gộp PDF (${formatInt(selectedTrackingCodes.length)})`}
                          </span>
                        </Button>
                      </>
                    ),
                  }
                : undefined
            }
            footer={
              orders.length > 0 ? (
                <>
                  <span className="tabular-nums">
                    Trang {formatInt(page)} / {formatInt(totalPages)} · Tổng {formatInt(total)} đơn
                  </span>
                  <div
                    className="flex flex-wrap items-center gap-1.5"
                    aria-label="Phân trang đơn hàng"
                  >
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      disabled={page <= 1 || loading}
                      onClick={() => setPage((p) => Math.max(1, p - 1))}
                    >
                      Trang trước
                    </Button>
                    {pageButtons.map((pNum, idx) => {
                      const prevNum = pageButtons[idx - 1];
                      const showGap = prevNum != null && pNum - prevNum > 1;
                      return (
                        <React.Fragment key={pNum}>
                          {showGap ? <span className="px-1 text-muted-foreground">…</span> : null}
                          <Button
                            type="button"
                            size="xs"
                            variant={pNum === page ? "default" : "outline"}
                            aria-current={pNum === page ? "page" : undefined}
                            disabled={loading}
                            onClick={() => setPage(pNum)}
                            className="min-w-7 tabular-nums"
                          >
                            {formatInt(pNum)}
                          </Button>
                        </React.Fragment>
                      );
                    })}
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      disabled={page >= totalPages || loading}
                      onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                    >
                      Trang sau
                    </Button>
                  </div>
                </>
              ) : null
            }
          >
            {/* Search & Filters Bar */}
            {filtersOpen ? (
              <div className="flex flex-wrap items-center gap-2 border-b border-border bg-muted/40 px-3 py-2.5 md:px-4">
                <div className="relative min-w-[200px] flex-1 sm:max-w-xs">
                  <Search
                    className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground"
                    aria-hidden="true"
                  />
                  <Input
                    type="search"
                    value={queryInput}
                    onChange={(e) => setQueryInput(e.target.value)}
                    placeholder="Tìm mã đơn, tên khách, mã vận đơn..."
                    aria-label="Tìm kiếm đơn hàng"
                    className="h-8 bg-card pl-8 text-[13px]"
                  />
                </div>

                <Select
                  value={carrierFilter}
                  onValueChange={(val) => {
                    setCarrierFilter(val);
                    setPage(1);
                  }}
                >
                  <SelectTrigger
                    aria-label="Lọc theo hãng vận chuyển"
                    className="h-8 w-full bg-card text-[13px] sm:w-auto sm:min-w-[160px]"
                  >
                    <SelectValue>
                      {carrierFilter === "all"
                        ? "Mọi hãng vận chuyển"
                        : (CARRIER_OPTIONS.find((c) => c.carrierCode === carrierFilter)
                            ?.carrierLabel ?? carrierFilter)}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">Mọi hãng vận chuyển</SelectItem>
                    {CARRIER_OPTIONS.map((c) => (
                      <SelectItem key={c.carrierCode} value={c.carrierCode}>
                        {c.carrierLabel}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>

                <Select
                  value={paymentFilter}
                  onValueChange={(val) => {
                    setPaymentFilter(val as "all" | "COD" | "VIETQR" | "ZALOPAY");
                    setPage(1);
                  }}
                >
                  <SelectTrigger
                    aria-label="Lọc theo phương thức thanh toán"
                    className="h-8 w-full bg-card text-[13px] sm:w-auto sm:min-w-[165px]"
                  >
                    <SelectValue>
                      {paymentFilter === "all"
                        ? "Mọi hình thức thanh toán"
                        : paymentFilter === "COD"
                          ? "Thu hộ COD"
                          : paymentFilter === "VIETQR"
                            ? "VietQR"
                            : "Ví ZaloPay"}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">Mọi hình thức thanh toán</SelectItem>
                    <SelectItem value="COD">Thu hộ COD</SelectItem>
                    <SelectItem value="VIETQR">VietQR</SelectItem>
                    <SelectItem value="ZALOPAY">Ví ZaloPay</SelectItem>
                  </SelectContent>
                </Select>

                <Select
                  value={channelFilter || CHANNEL_ALL_VALUE}
                  onValueChange={(val) => {
                    setChannelFilter((val === CHANNEL_ALL_VALUE ? "" : val) as SalesChannelFilter);
                    setPage(1);
                  }}
                >
                  <SelectTrigger
                    aria-label="Lọc theo kênh bán"
                    className="h-8 w-full bg-card text-[13px] sm:w-auto sm:min-w-[140px]"
                  >
                    <SelectValue>
                      {CHANNEL_FILTER_OPTIONS.find(
                        (opt) =>
                          (opt.value || CHANNEL_ALL_VALUE) === (channelFilter || CHANNEL_ALL_VALUE)
                      )?.label ?? "Mọi kênh bán"}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {CHANNEL_FILTER_OPTIONS.map((opt) => (
                      <SelectItem
                        key={opt.value || CHANNEL_ALL_VALUE}
                        value={opt.value || CHANNEL_ALL_VALUE}
                      >
                        {opt.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>

                <label className="inline-flex w-full items-center gap-1.5 text-xs text-muted-foreground sm:w-auto">
                  <span className="shrink-0">Hạn giao trước:</span>
                  <Input
                    type="date"
                    value={dueBeforeDate}
                    onChange={(e) => {
                      setDueBeforeDate(e.target.value);
                      setPage(1);
                    }}
                    aria-label="Lọc theo hạn giao trước ngày"
                    className="h-8 flex-1 bg-card text-[13px] tabular-nums sm:w-auto"
                  />
                </label>

                {hasActiveFilters ? (
                  <Button type="button" variant="ghost" size="xs" onClick={handleResetFilters}>
                    <X className="size-3.5" aria-hidden="true" />
                    <span>Xóa lọc</span>
                  </Button>
                ) : null}
              </div>
            ) : null}

            {orders.length === 0 ? (
              <div className="px-6 py-12 text-center">
                <p className="text-sm text-muted-foreground">
                  {counts.all === 0
                    ? "Gian hàng chưa phát sinh đơn hàng nào."
                    : "Không có đơn hàng nào khớp bộ lọc hiện tại."}
                </p>
                {hasActiveFilters ? (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="mt-3"
                    onClick={handleResetFilters}
                  >
                    Xóa bộ lọc
                  </Button>
                ) : null}
              </div>
            ) : (
              <>
                {/* Desktop Table */}
                <div className="hidden md:block">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead className="w-10">
                          <Checkbox
                            checked={allPageSelected}
                            onCheckedChange={() => handleToggleSelectPage()}
                            aria-label="Chọn tất cả đơn hàng trên trang này"
                          />
                        </TableHead>
                        <TableHead>Đơn hàng</TableHead>
                        <TableHead>Người nhận</TableHead>
                        <TableHead>Sản phẩm &amp; vận chuyển</TableHead>
                        <TableHead className="text-right">Thực nhận</TableHead>
                        <TableHead>Trạng thái</TableHead>
                        <TableHead className="sticky right-0 z-10 border-l border-border bg-card text-right">
                          Thao tác
                        </TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {orders.map((o) => {
                        const meta = getSellerOrderStatusMeta(
                          o.status,
                          o.shipment_job_status,
                          o.tracking_code
                        );
                        const tone = resolveSellerOrderTone(
                          o.status,
                          o.shipment_job_status,
                          o.tracking_code
                        );
                        const netVnd = computeSellerNetReceivable(o);
                        const isBusy = busyOrderId === o.id;
                        const canConfirm = canSellerConfirmOrder(o.status, o.shipment_job_status);
                        const jobStatus = (o.shipment_job_status ?? "").trim().toUpperCase();
                        const isQueuedOrFailed =
                          !o.tracking_code && (jobStatus === "QUEUED" || jobStatus === "FAILED");
                        const canShip = canSellerShipOrder(o.status, o.tracking_code);
                        const showShipDrawer = shippingOrderId === o.id;
                        const isSelected = selectedSet.has(o.id);
                        const firstPid = o.items?.[0]?.product_id;
                        const thumb = firstPid ? productImages[firstPid] : undefined;
                        const channel = normalizeSalesChannel(o.sales_channel);
                        const channelBadge = getSalesChannelMeta(channel);
                        const labelHref = buildShippingLabelHref(o.tracking_code);
                        const subText = `${formatPaymentMethodVi(o.payment_method)}${
                          o.carrier_code ? ` · ${formatCarrierLabel(o.carrier_code)}` : ""
                        }${o.tracking_code ? ` · ${o.tracking_code}` : ""}`;

                        return (
                          <React.Fragment key={o.id}>
                            <TableRow
                              data-state={isSelected ? "selected" : undefined}
                              className="group cursor-pointer"
                              onClick={() => setDrawerOrderId(o.id)}
                            >
                              <TableCell onClick={(e) => e.stopPropagation()}>
                                <Checkbox
                                  checked={isSelected}
                                  onCheckedChange={() => handleToggleSelectOrder(o)}
                                  aria-label={`Chọn đơn ${o.order_code}`}
                                />
                              </TableCell>
                              <TableCell>
                                <div className="flex items-center gap-2.5">
                                  {thumb ? (
                                    <img
                                      src={thumb}
                                      alt=""
                                      className="hidden size-9 shrink-0 rounded-md border border-border bg-muted object-cover xl:block"
                                    />
                                  ) : null}
                                  <div className="min-w-0">
                                    <div className="flex items-center gap-1.5">
                                      <button
                                        type="button"
                                        className="font-semibold text-foreground hover:text-brand hover:underline"
                                        onClick={(e) => {
                                          e.stopPropagation();
                                          setDrawerOrderId(o.id);
                                        }}
                                      >
                                        {o.order_code}
                                      </button>
                                      <Badge
                                        variant={channel === "STOREFRONT" ? "info" : "secondary"}
                                        className="rounded-sm px-1.5 py-0 text-[11px]"
                                      >
                                        {channelBadge.shortLabel}
                                      </Badge>
                                    </div>
                                    {o.created_at ? (
                                      <div className="text-xs text-muted-foreground tabular-nums">
                                        {formatDateTimeVi(o.created_at)}
                                      </div>
                                    ) : null}
                                  </div>
                                </div>
                              </TableCell>
                              <TableCell className="max-w-[140px]">
                                <div className="truncate font-medium text-foreground">
                                  {o.shipping_address?.recipient_name || "Người mua"}
                                </div>
                                <div className="truncate text-xs text-muted-foreground">
                                  {o.shipping_address?.province_name || "—"}
                                </div>
                              </TableCell>
                              <TableCell className="max-w-[200px] xl:max-w-[240px]">
                                <div
                                  className="truncate text-foreground"
                                  title={summarizeOrderItems(o.items)}
                                >
                                  {summarizeOrderItems(o.items)}
                                </div>
                                <div
                                  className="truncate text-xs text-muted-foreground"
                                  title={subText}
                                >
                                  {subText}
                                </div>
                              </TableCell>
                              <TableCell className="text-right tabular-nums">
                                <Money vnd={netVnd} />
                                <div className="text-xs text-muted-foreground">
                                  Hàng: {formatVnd(o.subtotal_vnd)}
                                </div>
                              </TableCell>
                              <TableCell>
                                <StatusBadge tone={tone}>{meta.label}</StatusBadge>
                              </TableCell>
                              <TableCell
                                className="sticky right-0 z-10 border-l border-border bg-card text-right group-hover:bg-accent/65"
                                onClick={(e) => e.stopPropagation()}
                              >
                                <div className="inline-flex flex-wrap items-center justify-end gap-1.5">
                                  {canConfirm ? (
                                    <Button
                                      type="button"
                                      variant={canShip ? "outline" : "default"}
                                      size="xs"
                                      disabled={isBusy}
                                      onClick={() => void handleConfirmOrder(o)}
                                    >
                                      {isBusy ? "..." : "Xác nhận"}
                                    </Button>
                                  ) : null}
                                  {canShip ? (
                                    <Button
                                      type="button"
                                      size="xs"
                                      variant={showShipDrawer ? "secondary" : "default"}
                                      disabled={isBusy}
                                      onClick={() => handleToggleShipDrawer(o)}
                                    >
                                      {showShipDrawer
                                        ? "Đóng"
                                        : isQueuedOrFailed
                                          ? "Thử tạo lại ngay"
                                          : "Tạo vận đơn"}
                                    </Button>
                                  ) : null}
                                  {labelHref ? (
                                    <Button variant="outline" size="xs" asChild>
                                      <a
                                        href={labelHref}
                                        target="_blank"
                                        rel="noopener noreferrer"
                                        title="Mở nhãn vận đơn ở tab mới — nhấn Ctrl+P để in"
                                      >
                                        In nhãn
                                      </a>
                                    </Button>
                                  ) : null}
                                  <Button variant="outline" size="xs" asChild>
                                    <Link href={`/seller/don-hang/${o.id}`}>Chi tiết</Link>
                                  </Button>
                                </div>
                              </TableCell>
                            </TableRow>
                            {showShipDrawer ? (
                              <TableRow className="bg-muted/50 hover:bg-muted/50">
                                <TableCell colSpan={7} className="py-3">
                                  <div className="flex flex-wrap items-center justify-between gap-3">
                                    <span className="text-[13px] text-muted-foreground">
                                      Tạo vận đơn cho đơn{" "}
                                      <strong className="text-foreground">{o.order_code}</strong>
                                    </span>
                                    <div className="flex flex-wrap items-center gap-2">
                                      <Select
                                        value={carrierCode}
                                        onValueChange={handleCarrierChange}
                                      >
                                        <SelectTrigger
                                          aria-label="Chọn đơn vị vận chuyển"
                                          className="h-8 w-auto min-w-[160px] bg-card text-xs"
                                        >
                                          <SelectValue>
                                            {activeCarrierOption.carrierLabel}
                                          </SelectValue>
                                        </SelectTrigger>
                                        <SelectContent>
                                          {carrierOptions.map((c) => (
                                            <SelectItem key={c.carrierCode} value={c.carrierCode}>
                                              {c.carrierLabel}
                                            </SelectItem>
                                          ))}
                                        </SelectContent>
                                      </Select>
                                      <Select value={serviceCode} onValueChange={setServiceCode}>
                                        <SelectTrigger
                                          aria-label="Chọn gói dịch vụ vận chuyển"
                                          className="h-8 w-auto min-w-[150px] bg-card text-xs"
                                        >
                                          <SelectValue>
                                            {activeCarrierOption.services.find(
                                              (s) => s.serviceCode === serviceCode
                                            )?.serviceLabel ?? serviceCode}
                                          </SelectValue>
                                        </SelectTrigger>
                                        <SelectContent>
                                          {activeCarrierOption.services.map((s) => (
                                            <SelectItem key={s.serviceCode} value={s.serviceCode}>
                                              {s.serviceLabel}
                                            </SelectItem>
                                          ))}
                                        </SelectContent>
                                      </Select>
                                      <Button
                                        type="button"
                                        size="xs"
                                        disabled={isBusy}
                                        onClick={() => void handleShipOrder(o)}
                                      >
                                        {isBusy ? "Đang tạo..." : "Xác nhận tạo vận đơn"}
                                      </Button>
                                    </div>
                                  </div>
                                </TableCell>
                              </TableRow>
                            ) : null}
                          </React.Fragment>
                        );
                      })}
                    </TableBody>
                  </Table>
                </div>

                {/* Mobile Card List (< 768px) */}
                <div className="md:hidden">
                  <div className="flex items-center gap-2 border-b border-border bg-muted/30 px-4 py-2.5">
                    <Checkbox
                      id="mobile-select-all-orders"
                      checked={allPageSelected}
                      onCheckedChange={() => handleToggleSelectPage()}
                    />
                    <label
                      htmlFor="mobile-select-all-orders"
                      className="cursor-pointer text-[13px] font-medium text-foreground"
                    >
                      Chọn tất cả trang này ({formatInt(orders.length)})
                    </label>
                  </div>

                  {orders.map((o) => {
                    const meta = getSellerOrderStatusMeta(
                      o.status,
                      o.shipment_job_status,
                      o.tracking_code
                    );
                    const tone = resolveSellerOrderTone(
                      o.status,
                      o.shipment_job_status,
                      o.tracking_code
                    );
                    const isBusy = busyOrderId === o.id;
                    const canConfirm = canSellerConfirmOrder(o.status, o.shipment_job_status);
                    const jobStatus = (o.shipment_job_status ?? "").trim().toUpperCase();
                    const isQueuedOrFailed =
                      !o.tracking_code && (jobStatus === "QUEUED" || jobStatus === "FAILED");
                    const canShip = canSellerShipOrder(o.status, o.tracking_code);
                    const showShipDrawer = shippingOrderId === o.id;
                    const isSelected = selectedSet.has(o.id);
                    const labelHref = buildShippingLabelHref(o.tracking_code);

                    return (
                      <div key={o.id} className="border-b border-border last:border-b-0">
                        <IndexRowCard
                          leading={
                            <Checkbox
                              checked={isSelected}
                              onCheckedChange={() => handleToggleSelectOrder(o)}
                              aria-label={`Chọn đơn ${o.order_code}`}
                            />
                          }
                          title={o.order_code}
                          badge={<StatusBadge tone={tone}>{meta.label}</StatusBadge>}
                          meta={
                            <>
                              <Money vnd={computeSellerNetReceivable(o)} size="sm" />
                              <span>·</span>
                              <span>{o.shipping_address?.recipient_name || "Người mua"}</span>
                              <span>·</span>
                              <span>{o.shipping_address?.province_name || "—"}</span>
                            </>
                          }
                          onClick={() => setDrawerOrderId(o.id)}
                          className="border-b-0 pb-2"
                        />
                        <div className="space-y-2 px-4 pb-3 pl-11">
                          <p className="line-clamp-2 text-xs text-muted-foreground">
                            {summarizeOrderItems(o.items)}
                            {o.tracking_code ? ` · ${o.tracking_code}` : ""}
                          </p>
                          <div className="flex flex-wrap items-center gap-2">
                            {canConfirm ? (
                              <Button
                                type="button"
                                variant={canShip ? "outline" : "default"}
                                size="xs"
                                className="min-h-10"
                                disabled={isBusy}
                                onClick={() => void handleConfirmOrder(o)}
                              >
                                {isBusy ? "..." : "Xác nhận"}
                              </Button>
                            ) : null}
                            {canShip ? (
                              <Button
                                type="button"
                                size="xs"
                                className="min-h-10"
                                variant={showShipDrawer ? "secondary" : "default"}
                                disabled={isBusy}
                                onClick={() => handleToggleShipDrawer(o)}
                              >
                                {showShipDrawer
                                  ? "Đóng"
                                  : isQueuedOrFailed
                                    ? "Thử tạo lại ngay"
                                    : "Tạo vận đơn"}
                              </Button>
                            ) : null}
                            {labelHref ? (
                              <Button variant="outline" size="xs" className="min-h-10" asChild>
                                <a href={labelHref} target="_blank" rel="noopener noreferrer">
                                  In nhãn
                                </a>
                              </Button>
                            ) : null}
                            <Button variant="outline" size="xs" className="min-h-10" asChild>
                              <Link href={`/seller/don-hang/${o.id}`}>Chi tiết</Link>
                            </Button>
                          </div>

                          {showShipDrawer ? (
                            <div className="space-y-2 rounded-lg border border-border bg-muted/50 p-3">
                              <Select value={carrierCode} onValueChange={handleCarrierChange}>
                                <SelectTrigger className="w-full bg-card">
                                  <SelectValue>{activeCarrierOption.carrierLabel}</SelectValue>
                                </SelectTrigger>
                                <SelectContent>
                                  {carrierOptions.map((c) => (
                                    <SelectItem key={c.carrierCode} value={c.carrierCode}>
                                      {c.carrierLabel}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                              <Select value={serviceCode} onValueChange={setServiceCode}>
                                <SelectTrigger className="w-full bg-card">
                                  <SelectValue>
                                    {activeCarrierOption.services.find(
                                      (s) => s.serviceCode === serviceCode
                                    )?.serviceLabel ?? serviceCode}
                                  </SelectValue>
                                </SelectTrigger>
                                <SelectContent>
                                  {activeCarrierOption.services.map((s) => (
                                    <SelectItem key={s.serviceCode} value={s.serviceCode}>
                                      {s.serviceLabel}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                              <Button
                                type="button"
                                size="sm"
                                className="min-h-10 w-full"
                                disabled={isBusy}
                                onClick={() => void handleShipOrder(o)}
                              >
                                {isBusy ? "Đang tạo..." : "Xác nhận tạo vận đơn"}
                              </Button>
                            </div>
                          ) : null}
                        </div>
                      </div>
                    );
                  })}
                </div>
              </>
            )}
          </IndexTable>
        </>
      ) : null}

      <SellerOrderQuickDrawer
        order={drawerOrder}
        actionBusy={drawerOrder ? busyOrderId === drawerOrder.id : false}
        onClose={() => setDrawerOrderId(null)}
        onAction={(order) => {
          const action = getNextBackendOrderAction(
            order.status,
            order.tracking_code,
            order.shipment_job_status
          );
          if (action?.kind === "confirm") {
            void handleConfirmOrder(order);
          } else if (action?.kind === "ship") {
            const defaults = resolveOrderShippingDefaults(order);
            void handleShipOrder(order, {
              carrier_code: defaults.carrierCode,
              service_code: defaults.serviceCode,
            });
          }
        }}
      />

      {/* Picking List Sheet */}
      <Sheet open={Boolean(pickingList)} onOpenChange={(open) => !open && setPickingList(null)}>
        <SheetContent side="right" className="w-full gap-0 p-0 sm:max-w-lg">
          <SheetHeader className="border-b border-border px-5 py-4 pr-12">
            <SheetTitle>Danh sách nhặt hàng theo SKU</SheetTitle>
            {pickingList ? (
              <SheetDescription className="tabular-nums">
                {formatInt(pickingList.order_count)} đơn hàng · Tổng{" "}
                {formatInt(pickingList.total_units)} sản phẩm
              </SheetDescription>
            ) : null}
          </SheetHeader>
          <div className="flex-1 overflow-y-auto px-5 py-4">
            {pickingList && pickingList.items.length === 0 ? (
              <p className="py-8 text-center text-sm text-muted-foreground">
                Không có sản phẩm nào trong các đơn đã chọn.
              </p>
            ) : pickingList ? (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Sản phẩm &amp; biến thể</TableHead>
                    <TableHead className="text-right">Tổng SL</TableHead>
                    <TableHead className="text-right">Số đơn</TableHead>
                    <TableHead>Mã đơn</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {pickingList.items.map((item) => (
                    <TableRow key={item.sku_id}>
                      <TableCell className="whitespace-normal">
                        <div className="font-medium text-foreground">{item.product_name}</div>
                        <div className="text-xs text-muted-foreground">{item.sku_name}</div>
                      </TableCell>
                      <TableCell className="text-right font-semibold tabular-nums">
                        {formatInt(item.total_quantity)}
                      </TableCell>
                      <TableCell className="text-right tabular-nums">
                        {formatInt(item.order_count)}
                      </TableCell>
                      <TableCell className="break-words whitespace-normal text-xs text-muted-foreground">
                        {(item.order_codes ?? []).join(", ") || "—"}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            ) : null}
          </div>
          <SheetFooter className="flex-row justify-end gap-2 border-t border-border px-5 py-3.5">
            <Button type="button" variant="outline" size="sm" onClick={() => setPickingList(null)}>
              Đóng
            </Button>
            <Button type="button" size="sm" onClick={() => window.print()}>
              <Printer className="size-3.5" aria-hidden="true" />
              <span>In danh sách</span>
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </div>
  );
}
