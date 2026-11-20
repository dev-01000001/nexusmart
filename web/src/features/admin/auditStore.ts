"use client";

import { useSyncExternalStore } from "react";
import {
  ADMIN_AUDIT_LOGS,
  INITIAL_ADMIN_CONTENT,
  INITIAL_ADMIN_LOYALTY,
  INITIAL_ADMIN_MODERATION,
  INITIAL_EXTENDED_PLATFORM_CONFIG,
} from "@/data";
import type {
  AdminContentState,
  AdminExtendedPlatformConfig,
  AdminLoyaltyState,
  AdminModerationState,
  AuditLogEntry,
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

const EMPTY_NOTES: Record<string, string> = {};
const EMPTY_DISPUTE_OVERRIDES: Record<string, DisputeLiveOverride> = {};

let auditLogsState: AuditLogEntry[] = [...ADMIN_AUDIT_LOGS];
let reconNotesState: Record<string, string> = {};
let extraConfigState: ExtraOpsConfig = { ...INITIAL_EXTRA_CONFIG };
let extendedConfigState: AdminExtendedPlatformConfig = INITIAL_EXTENDED_PLATFORM_CONFIG;
let disputeOverridesState: Record<string, DisputeLiveOverride> = {};
let contentState: AdminContentState = INITIAL_ADMIN_CONTENT;
let moderationState: AdminModerationState = INITIAL_ADMIN_MODERATION;
let loyaltyState: AdminLoyaltyState = INITIAL_ADMIN_LOYALTY;
let traceCounter = 100;

const auditListeners = new Set<() => void>();
const reconNoteListeners = new Set<() => void>();
const extraConfigListeners = new Set<() => void>();
const extendedConfigListeners = new Set<() => void>();
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
const subscribeReconNotes = makeSubscriber(reconNoteListeners);
const subscribeExtraConfig = makeSubscriber(extraConfigListeners);
const subscribeExtendedConfig = makeSubscriber(extendedConfigListeners);
const subscribeDisputeOverrides = makeSubscriber(disputeOverrideListeners);
const subscribeContent = makeSubscriber(contentListeners);
const subscribeModeration = makeSubscriber(moderationListeners);
const subscribeLoyalty = makeSubscriber(loyaltyListeners);

function nextTraceId(): string {
  traceCounter += 1;
  const hex = (0x99a00000 + traceCounter * 7919).toString(16);
  return `tr-${hex}`;
}

export function appendAdminAuditLog(input: AppendAuditInput): AuditLogEntry {
  const entry: AuditLogEntry = {
    id: `aud-live-${Date.now()}-${traceCounter}`,
    timestamp: new Date().toISOString(),
    actor: input.actor ?? "Trần Quốc Huy",
    role: input.role ?? "Quản trị viên",
    action: input.action,
    module: input.module,
    target: input.target,
    changeSummary: input.changeSummary,
    traceId: nextTraceId(),
  };
  auditLogsState = [entry, ...auditLogsState];
  emit(auditListeners);
  return entry;
}

export function setAdminReconNote(batchId: string, note: string): void {
  reconNotesState = {
    ...reconNotesState,
    [batchId]: note,
  };
  emit(reconNoteListeners);
}

export function updateAdminExtraConfig(next: ExtraOpsConfig): void {
  extraConfigState = { ...next };
  emit(extraConfigListeners);
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

export function useAdminAuditLogs(): AuditLogEntry[] {
  return useSyncExternalStore(
    subscribeAudit,
    () => auditLogsState,
    () => ADMIN_AUDIT_LOGS
  );
}

export function useAdminReconNotes(): Record<string, string> {
  return useSyncExternalStore(
    subscribeReconNotes,
    () => reconNotesState,
    () => EMPTY_NOTES
  );
}

export function useAdminExtraConfig(): ExtraOpsConfig {
  return useSyncExternalStore(
    subscribeExtraConfig,
    () => extraConfigState,
    () => INITIAL_EXTRA_CONFIG
  );
}

export function useAdminExtendedConfig(): AdminExtendedPlatformConfig {
  return useSyncExternalStore(
    subscribeExtendedConfig,
    () => extendedConfigState,
    () => INITIAL_EXTENDED_PLATFORM_CONFIG
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
