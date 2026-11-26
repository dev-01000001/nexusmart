"use client";

import React from "react";
import type { GatewayProvince, GatewayWard } from "@/lib/gateway-types";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

export interface PdpShippingSelectsProps {
  provinces: GatewayProvince[];
  wards: GatewayWard[];
  provinceCode: string;
  wardCode: string;
  loadingWards: boolean;
  onProvinceChange: (code: string) => void;
  onWardChange: (code: string) => void;
}

export function PdpShippingSelects({
  provinces,
  wards,
  provinceCode,
  wardCode,
  loadingWards,
  onProvinceChange,
  onWardChange,
}: PdpShippingSelectsProps) {
  return (
    <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
      <Select value={provinceCode || undefined} onValueChange={onProvinceChange}>
        <SelectTrigger
          aria-label="Chọn Tỉnh/Thành phố ước tính phí ship"
          className="h-10 w-full bg-card shadow-none"
        >
          <SelectValue placeholder="Chọn Tỉnh / Thành phố" />
        </SelectTrigger>
        <SelectContent>
          {provinces.map((p) => (
            <SelectItem key={p.code} value={p.code}>
              {p.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      <Select
        value={wardCode || undefined}
        onValueChange={onWardChange}
        disabled={!provinceCode || loadingWards}
      >
        <SelectTrigger
          aria-label="Chọn Phường/Xã ước tính phí ship"
          className="h-10 w-full bg-card shadow-none"
        >
          <SelectValue placeholder={loadingWards ? "Đang tải phường/xã..." : "Chọn Phường / Xã"} />
        </SelectTrigger>
        <SelectContent>
          {wards.map((w) => (
            <SelectItem key={w.code} value={w.code}>
              {w.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}
