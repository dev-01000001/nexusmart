"use client";

import { useSyncExternalStore } from "react";
import {
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
  VulnerablePriorityGroup,
} from "@/data";

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

let extraConfigState: ExtraOpsConfig = { ...INITIAL_EXTRA_CONFIG };
let extendedConfigState: AdminExtendedPlatformConfig = INITIAL_EXTENDED_PLATFORM_CONFIG;
let disputeOverridesState: Record<string, DisputeLiveOverride> = {};
let contentState: AdminContentState = INITIAL_ADMIN_CONTENT;
let moderationState: AdminModerationState = INITIAL_ADMIN_MODERATION;
let loyaltyState: AdminLoyaltyState = INITIAL_ADMIN_LOYALTY;

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

const subscribeExtraConfig = makeSubscriber(extraConfigListeners);
const subscribeExtendedConfig = makeSubscriber(extendedConfigListeners);
const subscribeDisputeOverrides = makeSubscriber(disputeOverrideListeners);
const subscribeContent = makeSubscriber(contentListeners);
const subscribeModeration = makeSubscriber(moderationListeners);
const subscribeLoyalty = makeSubscriber(loyaltyListeners);

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

export function useAdminLoyaltyState(): AdminLoyaltyState {
  return useSyncExternalStore(
    subscribeLoyalty,
    () => loyaltyState,
    () => INITIAL_ADMIN_LOYALTY
  );
}
