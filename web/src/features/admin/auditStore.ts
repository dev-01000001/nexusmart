"use client";

/**
 * In-memory mock state for Admin Portal screens/tabs that do NOT have backend RPCs yet:
 * - /admin/noi-dung (CMS banners, mega-menu, search weights, CSR campaigns)
 * - /admin/kiem-duyet mock tabs (reviews, Q&A, media moderation)
 * - /admin/thanh-vien mock tabs (loyalty tiers, referrals, gift cards)
 * - /admin/cau-hinh (platform config toggles & notification templates)
 * - /admin/khieu-nai (tier-2 dispute arbitration)
 *
 * Real backend entities (shop approvals, payment ledger & reconciliation, audit logs,
 * product review, platform vouchers & flash sales) come strictly from gateway via BFF.
 * Zero localStorage usage anywhere in this store.
 */
import { useSyncExternalStore } from "react";
import {
  ADMIN_DISPUTES,
  INITIAL_ADMIN_CONTENT,
  INITIAL_ADMIN_LOYALTY,
  INITIAL_ADMIN_MODERATION,
  INITIAL_EXTENDED_PLATFORM_CONFIG,
  INITIAL_PLATFORM_CONFIG,
} from "@/data";
import type {
  AdminContentState,
  AdminDispute,
  AdminExtendedPlatformConfig,
  AdminLoyaltyState,
  AdminModerationState,
  AuditLogEntry,
  PlatformConfigState,
  VulnerablePriorityGroup,
} from "@/data";

export interface AppendAuditInput {
  actor?: string;
  role?: AuditLogEntry["role"];
  action: string;
  module: AuditLogEntry["module"];
  target: string;
  changeSummary: string;
}

export interface ExtraOpsConfig {
  reconAlertThresholdVnd: number;
  shopApprovalSlaHours: number;
  autoCheckTaxAndPhoto: boolean;
}

export interface DisputeLiveOverride {
  ackConfirmedAt?: string;
  ackSlaHoursLeft?: number;
  vulnerableGroup?: VulnerablePriorityGroup | null;
}

const INITIAL_EXTRA_CONFIG: ExtraOpsConfig = {
  reconAlertThresholdVnd: 50000,
  shopApprovalSlaHours: 24,
  autoCheckTaxAndPhoto: true,
};

const EMPTY_DISPUTE_OVERRIDES: Record<string, DisputeLiveOverride> = {};

let mockAuditLogsState: AuditLogEntry[] = [];
let extraConfigState: ExtraOpsConfig = { ...INITIAL_EXTRA_CONFIG };
let platformConfigState: PlatformConfigState = INITIAL_PLATFORM_CONFIG;
let extendedConfigState: AdminExtendedPlatformConfig = INITIAL_EXTENDED_PLATFORM_CONFIG;
let disputesState: AdminDispute[] = [...ADMIN_DISPUTES];
let disputeOverridesState: Record<string, DisputeLiveOverride> = {};
let contentState: AdminContentState = INITIAL_ADMIN_CONTENT;
let moderationState: AdminModerationState = INITIAL_ADMIN_MODERATION;
let loyaltyState: AdminLoyaltyState = INITIAL_ADMIN_LOYALTY;
let traceCounter = 100;

const auditListeners = new Set<() => void>();
const extraConfigListeners = new Set<() => void>();
const platformConfigListeners = new Set<() => void>();
const extendedConfigListeners = new Set<() => void>();
const disputesListeners = new Set<() => void>();
const disputeOverrideListeners = new Set<() => void>();
const contentListeners = new Set<() => void>();
const moderationListeners = new Set<() => void>();
const loyaltyListeners = new Set<() => void>();

function emit(set: Set<() => void>) {
  for (const fn of set) fn();
}

function makeSubscriber(set: Set<() => void>) {
  return (callback: () => void) => {
    set.add(callback);
    return () => {
      set.delete(callback);
    };
  };
}

const subscribeAudit = makeSubscriber(auditListeners);
const subscribeExtraConfig = makeSubscriber(extraConfigListeners);
const subscribePlatformConfig = makeSubscriber(platformConfigListeners);
const subscribeExtendedConfig = makeSubscriber(extendedConfigListeners);
const subscribeDisputes = makeSubscriber(disputesListeners);
const subscribeDisputeOverrides = makeSubscriber(disputeOverrideListeners);
const subscribeContent = makeSubscriber(contentListeners);
const subscribeModeration = makeSubscriber(moderationListeners);
const subscribeLoyalty = makeSubscriber(loyaltyListeners);

function nextTraceId(): string {
  traceCounter += 1;
  const hex = (0x99a00000 + traceCounter * 7919).toString(16);
  return `tr-${hex}`;
}

/** Records in-memory mock actions performed on mock-only Admin tabs (never mixed into real /admin/audit-log). */
export function appendAdminAuditLog(input: AppendAuditInput): AuditLogEntry {
  const entry: AuditLogEntry = {
    id: `aud-mock-${Date.now()}-${traceCounter}`,
    timestamp: new Date().toISOString(),
    actor: input.actor ?? "Quản trị viên",
    role: input.role ?? "Quản trị viên",
    action: input.action,
    module: input.module,
    target: input.target,
    changeSummary: input.changeSummary,
    traceId: nextTraceId(),
  };
  mockAuditLogsState = [entry, ...mockAuditLogsState];
  emit(auditListeners);
  return entry;
}

export function updateAdminExtraConfig(next: ExtraOpsConfig): void {
  extraConfigState = { ...next };
  emit(extraConfigListeners);
}

export function updateAdminPlatformConfig(next: PlatformConfigState): void {
  platformConfigState = { ...next };
  emit(platformConfigListeners);
}

export function updateAdminExtendedConfig(
  updater:
    | AdminExtendedPlatformConfig
    | ((prev: AdminExtendedPlatformConfig) => AdminExtendedPlatformConfig)
): void {
  extendedConfigState =
    typeof updater === "function" ? updater(extendedConfigState) : { ...updater };
  emit(extendedConfigListeners);
}

export function updateAdminDisputeStatus(
  id: string,
  status: AdminDispute["status"],
  note?: string
): void {
  disputesState = disputesState.map((d) =>
    d.id === id ? { ...d, status, evidenceNote: note ?? d.evidenceNote } : d
  );
  emit(disputesListeners);
}

export function confirmDisputeAckSla(disputeId: string): void {
  disputeOverridesState = {
    ...disputeOverridesState,
    [disputeId]: {
      ...disputeOverridesState[disputeId],
      ackConfirmedAt: new Date().toISOString(),
      ackSlaHoursLeft: 0,
    },
  };
  emit(disputeOverrideListeners);
}

export function updateDisputePriorityGroup(
  disputeId: string,
  vulnerableGroup: VulnerablePriorityGroup | null
): void {
  disputeOverridesState = {
    ...disputeOverridesState,
    [disputeId]: {
      ...disputeOverridesState[disputeId],
      vulnerableGroup,
    },
  };
  emit(disputeOverrideListeners);
}

export function updateAdminContentState(
  updater: (prev: AdminContentState) => AdminContentState
): void {
  contentState = updater(contentState);
  emit(contentListeners);
}

export function updateAdminModerationState(
  updater: (prev: AdminModerationState) => AdminModerationState
): void {
  moderationState = updater(moderationState);
  emit(moderationListeners);
}

export function updateAdminLoyaltyState(
  updater: (prev: AdminLoyaltyState) => AdminLoyaltyState
): void {
  loyaltyState = updater(loyaltyState);
  emit(loyaltyListeners);
}

export function useMockAdminAuditLogs(): AuditLogEntry[] {
  return useSyncExternalStore(
    subscribeAudit,
    () => mockAuditLogsState,
    () => []
  );
}

export function useAdminExtraConfig(): ExtraOpsConfig {
  return useSyncExternalStore(
    subscribeExtraConfig,
    () => extraConfigState,
    () => INITIAL_EXTRA_CONFIG
  );
}

export function useAdminPlatformConfig(): PlatformConfigState {
  return useSyncExternalStore(
    subscribePlatformConfig,
    () => platformConfigState,
    () => INITIAL_PLATFORM_CONFIG
  );
}

export function useAdminExtendedConfig(): AdminExtendedPlatformConfig {
  return useSyncExternalStore(
    subscribeExtendedConfig,
    () => extendedConfigState,
    () => INITIAL_EXTENDED_PLATFORM_CONFIG
  );
}

export function useAdminDisputes(): AdminDispute[] {
  return useSyncExternalStore(
    subscribeDisputes,
    () => disputesState,
    () => ADMIN_DISPUTES
  );
}

export function useAdminDisputeOverrides(): Record<string, DisputeLiveOverride> {
  return useSyncExternalStore(
    subscribeDisputeOverrides,
    () => disputeOverridesState,
    () => EMPTY_DISPUTE_OVERRIDES
  );
}

export function useAdminContentState(): AdminContentState {
  return useSyncExternalStore(
    subscribeContent,
    () => contentState,
    () => INITIAL_ADMIN_CONTENT
  );
}

export function useAdminModerationState(): AdminModerationState {
  return useSyncExternalStore(
    subscribeModeration,
    () => moderationState,
    () => INITIAL_ADMIN_MODERATION
  );
}

export function usePendingModerationCount(): number {
  const mod = useAdminModerationState();
  const r = mod.reviews.filter((x) => x.status === "cho_duyet").length;
  const q = mod.qaItems.filter((x) => x.status === "cho_duyet").length;
  const m = mod.mediaItems.filter((x) => x.status === "cho_duyet").length;
  return r + q + m;
}

export function useAdminLoyaltyState(): AdminLoyaltyState {
  return useSyncExternalStore(
    subscribeLoyalty,
    () => loyaltyState,
    () => INITIAL_ADMIN_LOYALTY
  );
}
