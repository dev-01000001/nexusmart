"use client";

import React, { useMemo } from "react";
import { LineChart } from "@/components/app";
import { formatCompactVnd, formatInt, formatVnd } from "@/lib/format";

export interface TrendPoint {
  label: string;
  current: number;
  prior?: number;
}

export interface SellerTrendChartProps {
  ariaLabel: string;
  points: TrendPoint[];
  valueKind: "vnd" | "int";
  currentLegend: string;
  priorLegend?: string;
}

export function SellerTrendChart({
  ariaLabel,
  points,
  valueKind,
  currentLegend,
  priorLegend,
}: SellerTrendChartProps) {
  const labels = useMemo(() => points.map((p) => p.label), [points]);

  const series = useMemo(() => {
    const list: Array<{ name: string; values: number[]; dashed?: boolean }> = [
      {
        name: currentLegend,
        values: points.map((p) => p.current),
      },
    ];
    if (priorLegend && points.some((p) => typeof p.prior === "number")) {
      list.push({
        name: priorLegend,
        values: points.map((p) => p.prior ?? 0),
        dashed: true,
      });
    }
    return list;
  }, [points, currentLegend, priorLegend]);

  return (
    <LineChart
      labels={labels}
      series={series}
      height={224}
      showArea
      formatValue={valueKind === "vnd" ? formatVnd : formatInt}
      formatTick={valueKind === "vnd" ? formatCompactVnd : formatInt}
      ariaLabel={ariaLabel}
    />
  );
}
