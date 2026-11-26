"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { AlertTriangle, BookOpen, Lock, RefreshCw, Wallet } from "lucide-react";
import {
  EmptyState,
  ErrorState,
  IndexRowCard,
  IndexTable,
  MetricStrip,
  Money,
  PageHeader,
  StatusBadge,
  bankAccountStatusTone,
  orderStatusTone,
  shipmentJobTone,
  type StatusTone,
} from "@/components/app";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter } from "@/components/ui/card";
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableFooter,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, getErrorMessage } from "@/lib/api";
import {
  formatDateTimeVi,
  formatDateVi,
  formatInt,
  formatSignedVnd,
  formatVnd,
} from "@/lib/format";
import { getSalesChannelMeta } from "@/lib/salesChannel";
import { cn } from "@/lib/utils";
import { useSellerContext } from "./SellerShell";
import {
  computeSellerNetReceivable,
  computeSellerOrderDeductions,
  formatPaymentMethodVi,
  getSellerOrderStatusMeta,
  type GwSellerBalance,
  type GwSellerBankAccount,
  type GwSellerLedgerEntry,
  type GwSellerOrder,
} from "./sellerApiTypes";
import {
  BANK_ACCOUNT_STATUS_META,
  EMPTY_DASH,
  isTerminalBankOtpError,
  parseUiState,
  validateBankAccountInput,
} from "./types";

type FinanceTab = "ledger" | "bank";
type OrderFilter = "all" | "settled" | "pending";
type LedgerAccountFilter = "" | "pending" | "available";
const TRUE_MINUS = "\u2212";

const VN_BANK_PRESETS = [
  { code: "VCB", name: "Ngân hàng TMCP Ngoại thương Việt Nam (Vietcombank)" },
  { code: "TCB", name: "Ngân hàng TMCP Kỹ thương Việt Nam (Techcombank)" },
  { code: "ACB", name: "Ngân hàng TMCP Á Châu (ACB)" },
  { code: "MBB", name: "Ngân hàng TMCP Quân đội (MB Bank)" },
  { code: "BIDV", name: "Ngân hàng TMCP Đầu tư và Phát triển Việt Nam (BIDV)" },
  { code: "CTG", name: "Ngân hàng TMCP Công thương Việt Nam (VietinBank)" },
  { code: "VPB", name: "Ngân hàng TMCP Việt Nam Thịnh Vượng (VPBank)" },
  { code: "TPB", name: "Ngân hàng TMCP Tiên Phong (TPBank)" },
] as const;

function isOrderSettled(status: string): boolean {
  const st = status.toUpperCase();
  return st === "COMPLETED" || st === "DELIVERED";
}

function resolveOrderTone(order: GwSellerOrder): StatusTone {
  const job = (order.shipment_job_status ?? "").toUpperCase();
  if (!order.tracking_code && (job === "QUEUED" || job === "FAILED")) {
    return shipmentJobTone(job);
  }
  return orderStatusTone(order.status);
}

export interface SellerFinanceClientProps {
  initialBalance: GwSellerBalance | null;
  balanceError?: string | null;
  initialBankAccount: GwSellerBankAccount | null;
  initialOrders: GwSellerOrder[];
  loadError: string | null;
}

export function SellerFinanceClient({
  initialBalance,
  balanceError = null,
  initialBankAccount,
  initialOrders,
  loadError,
}: SellerFinanceClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const tabParam = searchParams.get("tab");
  const { showToast } = useSellerContext();

  const [activeTab, setActiveTab] = useState<FinanceTab>(
    tabParam === "bank" || tabParam === "ngan-hang" ? "bank" : "ledger"
  );
  const [balance, setBalance] = useState<GwSellerBalance | null>(initialBalance);
  const [bankAccount, setBankAccount] = useState<GwSellerBankAccount | null>(initialBankAccount);
  const [orderFilter, setOrderFilter] = useState<OrderFilter>("all");
  const [refreshing, setRefreshing] = useState(false);

  // Bank account form & Email OTP state
  const [showBankForm, setShowBankForm] = useState(false);
  const [bankCode, setBankCode] = useState<string>(VN_BANK_PRESETS[0].code);
  const [bankName, setBankName] = useState<string>(VN_BANK_PRESETS[0].name);
  const [accountNumber, setAccountNumber] = useState("");
  const [accountHolderName, setAccountHolderName] = useState("");
  const [bankFormError, setBankFormError] = useState<string | null>(null);
  const [bankSaving, setBankSaving] = useState(false);

  const [otpInput, setOtpInput] = useState("");
  const [otpError, setOtpError] = useState<string | null>(null);
  const [otpSubmitting, setOtpSubmitting] = useState(false);

  // Real double-entry ledger (/api/seller/ledger)
  const [ledgerEntries, setLedgerEntries] = useState<GwSellerLedgerEntry[]>([]);
  const [ledgerTotal, setLedgerTotal] = useState(0);
  const [ledgerPage, setLedgerPage] = useState(1);
  const [ledgerAccountFilter, setLedgerAccountFilter] = useState<LedgerAccountFilter>("");
  const [ledgerLoading, setLedgerLoading] = useState(true);
  const [ledgerError, setLedgerError] = useState<string | null>(null);
  const ledgerSeqRef = useRef(0);

  const loadLedger = useCallback(async () => {
    const seq = ++ledgerSeqRef.current;
    setLedgerLoading(true);
    setLedgerError(null);
    try {
      const qs = new URLSearchParams({
        page: String(ledgerPage),
        page_size: "20",
      });
      if (ledgerAccountFilter) {
        qs.set("account_filter", ledgerAccountFilter);
      }
      const res = await api<{ items?: GwSellerLedgerEntry[]; total?: number }>(
        `/api/seller/ledger?${qs.toString()}`
      );
      if (ledgerSeqRef.current !== seq) return;
      const items = res.items ?? [];
      setLedgerEntries(items);
      setLedgerTotal(Math.max(0, Math.trunc(res.total ?? items.length)));
    } catch (err) {
      if (ledgerSeqRef.current !== seq) return;
      setLedgerError(getErrorMessage(err, "Không thể tải sổ cái giao dịch lúc này."));
    } finally {
      if (ledgerSeqRef.current === seq) {
        setLedgerLoading(false);
      }
    }
  }, [ledgerPage, ledgerAccountFilter]);

  useEffect(() => {
    if (!uiState && !loadError) {
      void loadLedger();
    }
  }, [uiState, loadError, loadLedger]);

  const handleRefreshAll = async () => {
    if (refreshing) return;
    setRefreshing(true);
    try {
      const [balRes, bankRes] = await Promise.all([
        api<GwSellerBalance>("/api/seller/balance").catch(() => null),
        api<{ bank_account?: GwSellerBankAccount | null }>("/api/seller/bank-account").catch(
          () => null
        ),
        loadLedger(),
      ]);
      if (balRes) setBalance(balRes);
      if (bankRes) setBankAccount(bankRes.bank_account ?? null);
      router.refresh();
    } finally {
      setRefreshing(false);
    }
  };

  const handleSubmitBankAccount = async (e: React.FormEvent) => {
    e.preventDefault();
    if (bankSaving) return;
    const validation = validateBankAccountInput({
      bankCode,
      bankName,
      accountNumber,
      accountHolderName,
    });
    if (!validation.ok) {
      setBankFormError(validation.message);
      return;
    }

    setBankFormError(null);
    setBankSaving(true);
    try {
      const res = await api<{ bank_account?: GwSellerBankAccount | null }>(
        "/api/seller/bank-account",
        {
          method: "POST",
          body: {
            bank_code: bankCode.trim().toUpperCase(),
            bank_name: bankName.trim(),
            account_number: accountNumber.trim(),
            account_holder_name: accountHolderName.trim().toUpperCase(),
          },
        }
      );
      setBankAccount(res?.bank_account ?? null);
      setAccountNumber("");
      setOtpInput("");
      setOtpError(null);
      setShowBankForm(false);
      showToast("Đã ghi nhận tài khoản ngân hàng. Kiểm tra email để nhập mã OTP 6 chữ số.");
    } catch (err) {
      setBankFormError(getErrorMessage(err, "Không thể lưu tài khoản ngân hàng lúc này."));
    } finally {
      setBankSaving(false);
    }
  };

  const handleVerifyBankOtp = async (e: React.FormEvent) => {
    e.preventDefault();
    if (otpSubmitting) return;
    const cleanOtp = otpInput.trim();
    if (!/^\d{6}$/.test(cleanOtp)) {
      setOtpError("Mã OTP gồm đúng 6 chữ số gửi về email tài khoản.");
      return;
    }
    setOtpError(null);
    setOtpSubmitting(true);
    try {
      const res = await api<{ bank_account?: GwSellerBankAccount | null }>(
        "/api/seller/bank-account/verify-otp",
        {
          method: "POST",
          body: { otp: cleanOtp },
        }
      );
      const updated = res?.bank_account ?? null;
      setBankAccount(updated);
      setOtpInput("");
      if (updated?.status === "VERIFIED") {
        showToast("Đã xác thực OTP thành công. Tài khoản ngân hàng sẵn sàng nhận giải ngân.");
      } else if (updated?.status === "PENDING_ADMIN_REVIEW") {
        showToast(
          "Đã xác thực OTP. Tên chủ tài khoản khác tên chủ shop lúc được duyệt nên hồ sơ chờ quản trị duyệt."
        );
      } else {
        showToast("Đã xác thực mã OTP tài khoản ngân hàng.");
      }
    } catch (err) {
      if (isTerminalBankOtpError(err)) {
        const msg = getErrorMessage(
          err,
          "Mã OTP đã hết hạn hoặc nhập sai quá số lần quy định. Vui lòng gửi lại thông tin ngân hàng để nhận mã mới."
        );
        setOtpInput("");
        setOtpError(null);
        setBankAccount((prev) => (prev?.status === "PENDING_OTP" ? null : prev));
        setShowBankForm(true);
        setBankFormError(msg);
      } else {
        setOtpError(getErrorMessage(err, "Mã OTP chưa chính xác. Vui lòng kiểm tra lại."));
      }
    } finally {
      setOtpSubmitting(false);
    }
  };

  const nonCancelledOrders = useMemo(
    () =>
      initialOrders.filter((o) => {
        const st = o.status.toUpperCase();
        return st !== "CANCELLED" && st !== "RETURNED";
      }),
    [initialOrders]
  );

  const filteredOrders = useMemo(() => {
    if (orderFilter === "all") return nonCancelledOrders;
    if (orderFilter === "settled")
      return nonCancelledOrders.filter((o) => isOrderSettled(o.status));
    return nonCancelledOrders.filter((o) => !isOrderSettled(o.status));
  }, [nonCancelledOrders, orderFilter]);

  const totalDeductionsAll = useMemo(
    () => nonCancelledOrders.reduce((s, o) => s + computeSellerOrderDeductions(o), 0),
    [nonCancelledOrders]
  );
  const visibleGross = useMemo(
    () => filteredOrders.reduce((s, o) => s + o.subtotal_vnd, 0),
    [filteredOrders]
  );
  const visibleDeductions = useMemo(
    () => filteredOrders.reduce((s, o) => s + computeSellerOrderDeductions(o), 0),
    [filteredOrders]
  );
  const visibleNet = useMemo(
    () => filteredOrders.reduce((s, o) => s + computeSellerNetReceivable(o), 0),
    [filteredOrders]
  );

  const availableVnd = balance?.available_vnd ?? 0;
  const pendingVnd = balance?.pending_vnd ?? 0;
  const totalEarnedVnd = balance?.total_earned_vnd ?? 0;

  const bankStatusMeta = bankAccount
    ? (BANK_ACCOUNT_STATUS_META[bankAccount.status] ?? {
        label: bankAccount.status,
        tone: "neutral" as const,
      })
    : null;

  const isPayoutLocked = useMemo(() => {
    if (!bankAccount?.payout_locked_until) return false;
    const ms = Date.parse(bankAccount.payout_locked_until);
    return Number.isFinite(ms) && ms > Date.now();
  }, [bankAccount?.payout_locked_until]);

  const ledgerTotalPages = Math.max(1, Math.ceil(ledgerTotal / 20));

  return (
    <div className="space-y-5">
      <PageHeader
        title="Tài chính"
        subtitle="Theo dõi số dư khả dụng, bút toán sổ cái và tài khoản ngân hàng nhận giải ngân."
        primaryAction={
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={refreshing}
            onClick={() => void handleRefreshAll()}
          >
            <RefreshCw
              className={cn("size-3.5", refreshing && "animate-spin")}
              aria-hidden="true"
            />
            <span>{refreshing ? "Đang tải..." : "Làm mới"}</span>
          </Button>
        }
      />

      {uiState === "loading" && (
        <Card role="status" aria-label="Đang tải sổ cái tài chính...">
          <CardContent className="space-y-3 py-6">
            <Skeleton className="h-6 w-48" />
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </CardContent>
        </Card>
      )}

      {(uiState === "error" || (loadError && !uiState)) && (
        <ErrorState
          title="Không thể tải sổ cái đối soát"
          description={loadError ?? "Dịch vụ thanh toán tạm thời không phản hồi."}
          onRetry={() => router.refresh()}
        />
      )}

      {uiState === "empty" && (
        <EmptyState
          icon={Wallet}
          title="Chưa phát sinh bút toán đối soát"
          description="Doanh thu và phí sẽ ghi nhận tự động khi có đơn hàng phát sinh."
          action={
            <Button size="sm" asChild>
              <Link href="/seller/don-hang">Xem đơn hàng</Link>
            </Button>
          }
        />
      )}

      {!uiState && !loadError && (
        <>
          {balanceError && !balance && (
            <Alert>
              <AlertTriangle className="size-4" aria-hidden="true" />
              <AlertTitle>Chờ đồng bộ số dư</AlertTitle>
              <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
                <span>Dịch vụ thanh toán tạm gián đoạn, số dư khả dụng đang chờ đồng bộ.</span>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => void handleRefreshAll()}
                >
                  <RefreshCw className="size-3.5" aria-hidden="true" />
                  <span>Thử lại</span>
                </Button>
              </AlertDescription>
            </Alert>
          )}

          <section aria-label="Tổng hợp số dư gian hàng">
            <MetricStrip
              items={[
                {
                  label: "Khả dụng rút ngay",
                  value: balance ? <Money vnd={availableVnd} size="lg" /> : EMPTY_DASH,
                  hint: balance ? (
                    <span>
                      Tổng số dư:{" "}
                      <strong className="font-medium text-foreground tabular-nums">
                        {formatVnd(availableVnd + pendingVnd)}
                      </strong>
                    </span>
                  ) : (
                    "Chờ đồng bộ từ dịch vụ thanh toán"
                  ),
                },
                {
                  label: "Đang tạm giữ (72 giờ)",
                  value: balance ? <Money vnd={pendingVnd} size="lg" /> : EMPTY_DASH,
                },
                {
                  label: "Tổng thu tích lũy",
                  value: balance ? <Money vnd={totalEarnedVnd} size="lg" /> : EMPTY_DASH,
                },
                {
                  label: `Chiết khấu & phí (${formatInt(nonCancelledOrders.length)} đơn)`,
                  value: <Money vnd={totalDeductionsAll} size="lg" />,
                },
              ]}
            />
          </section>

          <Tabs
            value={activeTab}
            onValueChange={(v) => setActiveTab(v as FinanceTab)}
            className="space-y-5"
          >
            <TabsList
              aria-label="Phân mục tài chính"
              className="h-auto w-full justify-start border-b border-border bg-transparent pb-2"
            >
              <TabsTrigger value="ledger" className="min-h-10 gap-1.5 md:min-h-8">
                <span>Sổ cái &amp; Đối soát</span>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {formatInt(ledgerTotal)}
                </span>
              </TabsTrigger>
              <TabsTrigger value="bank" className="min-h-10 gap-1.5 md:min-h-8">
                <span>Tài khoản ngân hàng</span>
                {bankAccount?.status === "PENDING_OTP" ? (
                  <Badge variant="attention">Chờ OTP</Badge>
                ) : null}
              </TabsTrigger>
            </TabsList>

            <TabsContent value="ledger" className="space-y-6">
              {/* Real Seller Double-Entry Ledger (/api/seller/ledger) */}
              <section aria-labelledby="seller-ledger-heading" className="space-y-2.5">
                <div>
                  <h2 id="seller-ledger-heading" className="text-sm font-semibold text-foreground">
                    Sổ cái giao dịch ({formatInt(ledgerTotal)})
                  </h2>
                  <p className="text-[13px] text-muted-foreground">
                    Bút toán trên tài khoản tạm giữ (pending) và khả dụng (available).
                  </p>
                </div>

                <IndexTable
                  tabs={[
                    { key: "all", label: "Tất cả" },
                    { key: "pending", label: "Đang tạm giữ" },
                    { key: "available", label: "Khả dụng" },
                  ]}
                  activeTab={ledgerAccountFilter || "all"}
                  onTabChange={(k) => {
                    setLedgerAccountFilter(k === "all" ? "" : (k as LedgerAccountFilter));
                    setLedgerPage(1);
                  }}
                  footer={
                    ledgerTotalPages > 1 ? (
                      <>
                        <span className="tabular-nums">
                          Trang {formatInt(ledgerPage)} / {formatInt(ledgerTotalPages)}
                        </span>
                        <div className="flex items-center gap-2">
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={ledgerPage <= 1}
                            onClick={() => setLedgerPage((p) => Math.max(1, p - 1))}
                          >
                            Trang trước
                          </Button>
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={ledgerPage >= ledgerTotalPages}
                            onClick={() => setLedgerPage((p) => Math.min(ledgerTotalPages, p + 1))}
                          >
                            Trang sau
                          </Button>
                        </div>
                      </>
                    ) : undefined
                  }
                >
                  {ledgerLoading ? (
                    <div
                      className="space-y-3 p-4"
                      role="status"
                      aria-label="Đang tải sổ cái giao dịch..."
                    >
                      {Array.from({ length: 5 }).map((_, idx) => (
                        <Skeleton key={idx} className="h-10 w-full" />
                      ))}
                    </div>
                  ) : ledgerError ? (
                    <div className="p-4">
                      <ErrorState
                        title="Không thể tải sổ cái giao dịch"
                        description={ledgerError}
                        onRetry={() => void loadLedger()}
                        className="shadow-none"
                      />
                    </div>
                  ) : ledgerEntries.length === 0 ? (
                    <div className="p-4">
                      <EmptyState
                        icon={BookOpen}
                        title="Chưa có bút toán nào trong tài khoản đã chọn"
                        description="Khi đơn hàng được thanh toán hoặc đối soát, bút toán sẽ hiển thị tại đây."
                        className="shadow-none"
                      />
                    </div>
                  ) : (
                    <>
                      <div className="hidden md:block">
                        <Table>
                          <TableHeader>
                            <TableRow>
                              <TableHead>Thời gian</TableHead>
                              <TableHead>Tài khoản</TableHead>
                              <TableHead>Tham chiếu</TableHead>
                              <TableHead>Diễn giải</TableHead>
                              <TableHead className="text-right">Ghi nợ</TableHead>
                              <TableHead className="text-right">Ghi có</TableHead>
                              <TableHead className="text-right">Biến động</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {ledgerEntries.map((entry) => (
                              <TableRow key={entry.id}>
                                <TableCell className="text-muted-foreground tabular-nums">
                                  {entry.created_at
                                    ? formatDateTimeVi(entry.created_at)
                                    : EMPTY_DASH}
                                </TableCell>
                                <TableCell>
                                  <Badge variant="neutral">{entry.account_code}</Badge>
                                </TableCell>
                                <TableCell className="whitespace-normal">
                                  <div className="font-medium text-foreground">
                                    {entry.reference_type || EMPTY_DASH}
                                  </div>
                                  <div className="text-xs text-muted-foreground">
                                    {entry.reference_id || EMPTY_DASH}
                                  </div>
                                </TableCell>
                                <TableCell className="max-w-[260px] whitespace-normal text-foreground">
                                  {entry.description || EMPTY_DASH}
                                </TableCell>
                                <TableCell className="text-right text-destructive tabular-nums">
                                  {entry.debit_vnd > 0 ? formatVnd(entry.debit_vnd) : EMPTY_DASH}
                                </TableCell>
                                <TableCell className="text-right text-badge-success-ink tabular-nums">
                                  {entry.credit_vnd > 0 ? formatVnd(entry.credit_vnd) : EMPTY_DASH}
                                </TableCell>
                                <TableCell
                                  className={cn(
                                    "text-right font-semibold tabular-nums",
                                    entry.delta_vnd >= 0
                                      ? "text-badge-success-ink"
                                      : "text-destructive"
                                  )}
                                >
                                  {formatSignedVnd(entry.delta_vnd)}
                                </TableCell>
                              </TableRow>
                            ))}
                          </TableBody>
                        </Table>
                      </div>

                      <div className="md:hidden">
                        {ledgerEntries.map((entry) => (
                          <IndexRowCard
                            key={entry.id}
                            title={entry.reference_type || entry.account_code}
                            badge={
                              <span
                                className={cn(
                                  "text-sm font-semibold tabular-nums",
                                  entry.delta_vnd >= 0
                                    ? "text-badge-success-ink"
                                    : "text-destructive"
                                )}
                              >
                                {formatSignedVnd(entry.delta_vnd)}
                              </span>
                            }
                            meta={
                              <>
                                <span>
                                  {entry.created_at
                                    ? formatDateTimeVi(entry.created_at)
                                    : EMPTY_DASH}
                                </span>
                                <span>·</span>
                                <span>{entry.account_code}</span>
                                {(entry.description || entry.reference_id) && (
                                  <>
                                    <span>·</span>
                                    <span className="truncate">
                                      {entry.description || entry.reference_id}
                                    </span>
                                  </>
                                )}
                              </>
                            }
                          />
                        ))}
                      </div>
                    </>
                  )}
                </IndexTable>
              </section>

              {/* Order Revenue Reconciliation */}
              <section aria-labelledby="order-recon-heading" className="space-y-2.5">
                <div>
                  <h2 id="order-recon-heading" className="text-sm font-semibold text-foreground">
                    Đối chiếu doanh thu theo đơn hàng
                  </h2>
                  <p className="text-[13px] text-muted-foreground tabular-nums">
                    Tiền hàng ({formatVnd(visibleGross)}) {TRUE_MINUS} Chiết khấu &amp; phí (
                    {formatVnd(visibleDeductions)}) = Thực nhận dự kiến{" "}
                    <strong className="font-semibold text-foreground">
                      {formatVnd(visibleNet)}
                    </strong>
                  </p>
                </div>

                <IndexTable
                  tabs={[
                    { key: "all", label: "Tất cả", count: nonCancelledOrders.length },
                    { key: "settled", label: "Đã giao" },
                    { key: "pending", label: "Đang xử lý" },
                  ]}
                  activeTab={orderFilter}
                  onTabChange={(k) => setOrderFilter(k as OrderFilter)}
                >
                  {filteredOrders.length === 0 ? (
                    <div className="p-4">
                      <EmptyState
                        icon={Wallet}
                        title="Không có đơn hàng trong bộ lọc đối soát hiện tại"
                        description="Chuyển sang tab khác hoặc chờ phát sinh đơn hàng mới."
                        className="shadow-none"
                      />
                    </div>
                  ) : (
                    <>
                      <div className="hidden md:block">
                        <Table>
                          <TableHeader>
                            <TableRow>
                              <TableHead>Ngày</TableHead>
                              <TableHead>Mã đơn</TableHead>
                              <TableHead>Thanh toán</TableHead>
                              <TableHead className="text-right">Tiền hàng</TableHead>
                              <TableHead className="text-right">Chiết khấu &amp; phí</TableHead>
                              <TableHead className="text-right">Thực nhận</TableHead>
                              <TableHead>Trạng thái</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {filteredOrders.map((o) => {
                              const meta = getSellerOrderStatusMeta(
                                o.status,
                                o.shipment_job_status,
                                o.tracking_code
                              );
                              const channelMeta = getSalesChannelMeta(o.sales_channel);
                              const deductions = computeSellerOrderDeductions(o);
                              const net = computeSellerNetReceivable(o);
                              return (
                                <TableRow key={o.id}>
                                  <TableCell className="text-muted-foreground tabular-nums">
                                    {o.created_at ? formatDateVi(o.created_at) : EMPTY_DASH}
                                  </TableCell>
                                  <TableCell>
                                    <Link
                                      href={`/seller/don-hang/${o.id}`}
                                      className="font-semibold text-brand hover:underline"
                                    >
                                      {o.order_code}
                                    </Link>
                                    <div className="text-xs text-muted-foreground">
                                      {channelMeta.label}
                                    </div>
                                  </TableCell>
                                  <TableCell>{formatPaymentMethodVi(o.payment_method)}</TableCell>
                                  <TableCell className="text-right text-badge-success-ink tabular-nums">
                                    +{formatVnd(o.subtotal_vnd)}
                                  </TableCell>
                                  <TableCell
                                    className={cn(
                                      "text-right tabular-nums",
                                      deductions > 0 ? "text-destructive" : "text-muted-foreground"
                                    )}
                                  >
                                    {deductions > 0
                                      ? `${TRUE_MINUS}${formatVnd(deductions)}`
                                      : EMPTY_DASH}
                                  </TableCell>
                                  <TableCell className="text-right font-semibold text-foreground tabular-nums">
                                    {formatVnd(net)}
                                  </TableCell>
                                  <TableCell>
                                    <StatusBadge tone={resolveOrderTone(o)}>
                                      {meta.label}
                                    </StatusBadge>
                                  </TableCell>
                                </TableRow>
                              );
                            })}
                          </TableBody>
                          <TableFooter>
                            <TableRow>
                              <TableCell colSpan={3}>
                                Tổng ({formatInt(filteredOrders.length)} đơn)
                              </TableCell>
                              <TableCell className="text-right text-badge-success-ink tabular-nums">
                                +{formatVnd(visibleGross)}
                              </TableCell>
                              <TableCell className="text-right tabular-nums">
                                {visibleDeductions > 0
                                  ? `${TRUE_MINUS}${formatVnd(visibleDeductions)}`
                                  : "0₫"}
                              </TableCell>
                              <TableCell className="text-right font-semibold tabular-nums">
                                {formatVnd(visibleNet)}
                              </TableCell>
                              <TableCell />
                            </TableRow>
                          </TableFooter>
                        </Table>
                      </div>

                      <div className="md:hidden">
                        {filteredOrders.map((o) => {
                          const meta = getSellerOrderStatusMeta(
                            o.status,
                            o.shipment_job_status,
                            o.tracking_code
                          );
                          const deductions = computeSellerOrderDeductions(o);
                          const net = computeSellerNetReceivable(o);
                          return (
                            <IndexRowCard
                              key={o.id}
                              href={`/seller/don-hang/${o.id}`}
                              title={o.order_code}
                              badge={
                                <StatusBadge tone={resolveOrderTone(o)}>{meta.label}</StatusBadge>
                              }
                              meta={
                                <>
                                  <span className="font-semibold text-badge-success-ink tabular-nums">
                                    +{formatVnd(net)}
                                  </span>
                                  <span>·</span>
                                  <span className="tabular-nums">
                                    Phí &amp; giảm giá: {formatVnd(deductions)}
                                  </span>
                                  <span>·</span>
                                  <span>
                                    {o.created_at ? formatDateVi(o.created_at) : EMPTY_DASH},{" "}
                                    {formatPaymentMethodVi(o.payment_method)}
                                  </span>
                                </>
                              }
                            />
                          );
                        })}
                      </div>
                    </>
                  )}
                </IndexTable>
              </section>
            </TabsContent>

            {/* Bank Account + Email OTP + 24h Payout Lock (A5: 800px, 1/3 + 2/3) */}
            <TabsContent value="bank">
              <div className="mx-auto w-full max-w-[800px] space-y-6">
                <section
                  aria-labelledby="bank-account-heading"
                  className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
                >
                  <div className="space-y-2">
                    <h2 id="bank-account-heading" className="text-sm font-semibold text-foreground">
                      Tài khoản ngân hàng nhận tiền
                    </h2>
                    <p className="text-[13px] leading-[18px] text-muted-foreground">
                      Xác thực OTP qua email và khóa giải ngân 24 giờ khi thay đổi.
                    </p>
                    <Button
                      type="button"
                      variant={showBankForm ? "outline" : "default"}
                      size="sm"
                      onClick={() => {
                        setBankFormError(null);
                        setShowBankForm((prev) => !prev);
                      }}
                    >
                      {showBankForm
                        ? "Đóng biểu mẫu"
                        : bankAccount
                          ? "Cập nhật tài khoản"
                          : "Liên kết ngân hàng"}
                    </Button>
                  </div>

                  <Card>
                    <CardContent className="space-y-4 pt-4">
                      {bankAccount ? (
                        <div className="space-y-3">
                          <div className="flex flex-wrap items-start justify-between gap-2">
                            <div className="text-sm font-semibold text-foreground">
                              {bankAccount.bank_code} · {bankAccount.bank_name} ·{" "}
                              <span className="tabular-nums">
                                {bankAccount.account_number_masked}
                              </span>
                            </div>
                            {bankStatusMeta && (
                              <StatusBadge tone={bankAccountStatusTone(bankAccount.status)}>
                                {bankStatusMeta.label}
                              </StatusBadge>
                            )}
                          </div>

                          <div className="text-[13px] text-muted-foreground">
                            Chủ tài khoản:{" "}
                            <strong className="font-medium text-foreground">
                              {bankAccount.account_holder_name}
                            </strong>{" "}
                            ·{" "}
                            {bankAccount.holder_name_matched
                              ? "Khớp tên chủ shop"
                              : "Khác tên chủ shop lúc được duyệt (chờ quản trị duyệt sau OTP)"}
                          </div>

                          {bankAccount.verified_at && (
                            <div className="text-xs text-muted-foreground tabular-nums">
                              Xác minh lúc: {formatDateTimeVi(bankAccount.verified_at)}
                            </div>
                          )}

                          {bankAccount.review_note && (
                            <p
                              className={cn(
                                "text-[13px]",
                                bankAccount.status === "REJECTED"
                                  ? "text-destructive"
                                  : "text-muted-foreground"
                              )}
                            >
                              {bankAccount.status === "REJECTED"
                                ? "Lý do từ chối: "
                                : "Ghi chú duyệt: "}
                              {bankAccount.review_note}
                            </p>
                          )}

                          {isPayoutLocked && bankAccount.payout_locked_until && (
                            <Alert>
                              <Lock className="size-4" aria-hidden="true" />
                              <AlertDescription>
                                <p>
                                  Tài khoản ngân hàng vừa thay đổi — lệnh giải ngân tạm khóa 24 giờ
                                  đến{" "}
                                  <strong className="font-medium text-foreground tabular-nums">
                                    {formatDateTimeVi(bankAccount.payout_locked_until)}
                                  </strong>
                                  .
                                </p>
                              </AlertDescription>
                            </Alert>
                          )}
                        </div>
                      ) : (
                        <p className="text-[13px] text-muted-foreground">
                          Chưa liên kết tài khoản ngân hàng nhận giải ngân.
                        </p>
                      )}

                      {bankAccount?.status === "PENDING_OTP" && (
                        <>
                          <Separator />
                          <form
                            className="space-y-3"
                            onSubmit={(e) => void handleVerifyBankOtp(e)}
                            aria-label="Xác thực OTP tài khoản ngân hàng"
                          >
                            <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
                              <Field
                                className="flex-1"
                                data-invalid={Boolean(otpError) || undefined}
                              >
                                <FieldLabel htmlFor="bank-otp">
                                  Mã OTP 6 chữ số (gửi về email)
                                </FieldLabel>
                                <Input
                                  id="bank-otp"
                                  type="text"
                                  inputMode="numeric"
                                  maxLength={6}
                                  value={otpInput}
                                  onChange={(e) =>
                                    setOtpInput(e.target.value.replace(/\D/g, "").slice(0, 6))
                                  }
                                  placeholder="123456"
                                  required
                                  className="tabular-nums"
                                  aria-invalid={Boolean(otpError)}
                                />
                              </Field>
                              <Button type="submit" size="default" disabled={otpSubmitting}>
                                {otpSubmitting ? "Đang xác thực..." : "Xác thực OTP"}
                              </Button>
                            </div>
                            {otpError ? <FieldError>{otpError}</FieldError> : null}
                          </form>
                        </>
                      )}
                    </CardContent>
                  </Card>
                </section>

                {showBankForm && (
                  <>
                    <Separator />
                    <section className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
                      <div className="space-y-1">
                        <h3 className="text-sm font-semibold text-foreground">
                          {bankAccount ? "Cập nhật tài khoản ngân hàng" : "Liên kết tài khoản mới"}
                        </h3>
                        <p className="text-[13px] leading-[18px] text-muted-foreground">
                          Sau khi lưu, hệ thống gửi mã OTP 6 chữ số tới email đăng nhập của chủ gian
                          hàng để xác nhận.
                        </p>
                      </div>

                      <Card>
                        <form
                          onSubmit={(e) => void handleSubmitBankAccount(e)}
                          aria-label="Biểu mẫu liên kết tài khoản ngân hàng"
                        >
                          <CardContent className="pt-4">
                            <FieldGroup className="gap-4">
                              <Field>
                                <FieldLabel htmlFor="bank-preset">Ngân hàng</FieldLabel>
                                <Select
                                  value={bankCode}
                                  onValueChange={(code) => {
                                    setBankCode(code);
                                    const preset = VN_BANK_PRESETS.find((b) => b.code === code);
                                    if (preset) setBankName(preset.name);
                                  }}
                                >
                                  <SelectTrigger id="bank-preset" className="w-full bg-card">
                                    <SelectValue>
                                      {(() => {
                                        const preset = VN_BANK_PRESETS.find(
                                          (b) => b.code === bankCode
                                        );
                                        return preset
                                          ? `${preset.code} — ${preset.name}`
                                          : bankCode;
                                      })()}
                                    </SelectValue>
                                  </SelectTrigger>
                                  <SelectContent>
                                    {VN_BANK_PRESETS.map((b) => (
                                      <SelectItem key={b.code} value={b.code}>
                                        {b.code} — {b.name}
                                      </SelectItem>
                                    ))}
                                  </SelectContent>
                                </Select>
                              </Field>

                              <Field>
                                <FieldLabel htmlFor="bank-name">Tên ngân hàng</FieldLabel>
                                <Input
                                  id="bank-name"
                                  type="text"
                                  value={bankName}
                                  onChange={(e) => setBankName(e.target.value)}
                                  required
                                />
                              </Field>

                              <Field>
                                <FieldLabel htmlFor="bank-number">
                                  Số tài khoản (6–19 chữ số)
                                </FieldLabel>
                                <Input
                                  id="bank-number"
                                  type="text"
                                  inputMode="numeric"
                                  value={accountNumber}
                                  onChange={(e) =>
                                    setAccountNumber(e.target.value.replace(/\D/g, ""))
                                  }
                                  placeholder="Nhập số tài khoản"
                                  required
                                  className="tabular-nums"
                                />
                              </Field>

                              <Field>
                                <FieldLabel htmlFor="bank-holder">
                                  Tên chủ tài khoản (không dấu)
                                </FieldLabel>
                                <Input
                                  id="bank-holder"
                                  type="text"
                                  value={accountHolderName}
                                  onChange={(e) => setAccountHolderName(e.target.value)}
                                  placeholder="NGUYEN VAN A"
                                  required
                                />
                              </Field>

                              {bankFormError ? <FieldError>{bankFormError}</FieldError> : null}
                            </FieldGroup>
                          </CardContent>
                          <CardFooter className="justify-end gap-2 border-t border-border pt-3">
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              onClick={() => setShowBankForm(false)}
                            >
                              Hủy
                            </Button>
                            <Button type="submit" size="sm" disabled={bankSaving}>
                              {bankSaving ? "Đang gửi OTP..." : "Lưu & gửi mã OTP"}
                            </Button>
                          </CardFooter>
                        </form>
                      </Card>
                    </section>
                  </>
                )}
              </div>
            </TabsContent>
          </Tabs>
        </>
      )}
    </div>
  );
}
