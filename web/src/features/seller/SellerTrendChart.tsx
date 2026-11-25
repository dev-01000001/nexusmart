"use client";

import React, { useId, useMemo, useState } from "react";
import { formatInt, formatVnd } from "@/lib/format";
import styles from "./SellerTrendChart.module.css";

export interface TrendPoint {
  label: string;
  current: number;
  prior?: number;
}

interface SellerTrendChartProps {
  ariaLabel: string;
  points: TrendPoint[];
  valueKind: "vnd" | "int";
  currentLegend: string;
  priorLegend?: string;
}

function formatAxisTick(value: number, kind: "vnd" | "int"): string {
  if (kind === "int") return formatInt(Math.round(value));
  if (value >= 1_000_000) {
    const m = value / 1_000_000;
    return `${m % 1 === 0 ? m.toFixed(0) : m.toFixed(1).replace(".", ",")}tr`;
  }
  if (value >= 1_000) {
    return `${Math.round(value / 1_000)}k`;
  }
  return `${Math.round(value)}₫`;
}

function niceCeiling(rawMax: number, kind: "vnd" | "int"): number {
  if (rawMax <= 0) return kind === "vnd" ? 1_000_000 : 10;
  const step =
    kind === "vnd"
      ? rawMax >= 10_000_000
        ? 5_000_000
        : rawMax >= 1_000_000
          ? 500_000
          : 100_000
      : rawMax >= 100
        ? 50
        : rawMax >= 20
          ? 10
          : 5;
  return Math.ceil((rawMax * 1.12) / step) * step;
}

export function SellerTrendChart({
  ariaLabel,
  points,
  valueKind,
  currentLegend,
  priorLegend,
}: SellerTrendChartProps) {
  const [hoveredIdx, setHoveredIdx] = useState<number | null>(null);
  const gradId = useId().replace(/:/g, "");

  const width = 680;
  const height = 224;
  const pad = { top: 14, right: 16, bottom: 28, left: 48 };
  const plotW = width - pad.left - pad.right;
  const plotH = height - pad.top - pad.bottom;

  const hasPrior = useMemo(
    () => Boolean(priorLegend) && points.some((p) => typeof p.prior === "number"),
    [points, priorLegend]
  );

  const { yMax, yTicks, currentCoords, priorCoords, xLabelIndices } = useMemo(() => {
    const allVals = points.flatMap((p) =>
      typeof p.prior === "number" ? [p.current, p.prior] : [p.current]
    );
    const maxVal = Math.max(0, ...allVals);
    const ceiling = niceCeiling(maxVal, valueKind);
    const ticks = [0, 0.25, 0.5, 0.75, 1].map((r) => ceiling * r);

    const n = Math.max(1, points.length);
    const xFor = (i: number) => (n === 1 ? pad.left + plotW / 2 : pad.left + (i / (n - 1)) * plotW);
    const yFor = (v: number) => pad.top + plotH - (v / ceiling) * plotH;

    const cCoords = points.map((p, i) => ({ x: xFor(i), y: yFor(p.current), v: p.current }));
    const pCoords = points.map((p, i) => ({
      x: xFor(i),
      y: yFor(p.prior ?? 0),
      v: p.prior ?? 0,
    }));

    const maxLabels = points.length <= 10 ? points.length : 7;
    const step = Math.max(1, Math.ceil((points.length - 1) / Math.max(1, maxLabels - 1)));
    const labelIdx = new Set<number>();
    for (let i = 0; i < points.length; i += step) {
      labelIdx.add(i);
    }
    if (points.length > 0) {
      labelIdx.add(points.length - 1);
    }

    return {
      yMax: ceiling,
      yTicks: ticks,
      currentCoords: cCoords,
      priorCoords: pCoords,
      xLabelIndices: labelIdx,
    };
  }, [points, valueKind, pad.left, pad.top, plotW, plotH]);

  const toPolyline = (coords: Array<{ x: number; y: number }>) =>
    coords.map((c) => `${c.x.toFixed(1)},${c.y.toFixed(1)}`).join(" ");

  const areaPath = useMemo(() => {
    if (currentCoords.length < 2) return "";
    const baseY = (pad.top + plotH).toFixed(1);
    const first = currentCoords[0];
    const last = currentCoords[currentCoords.length - 1];
    const lineSegs = currentCoords
      .map((c, idx) => `${idx === 0 ? "M" : "L"}${c.x.toFixed(1)},${c.y.toFixed(1)}`)
      .join(" ");
    return `${lineSegs} L${last.x.toFixed(1)},${baseY} L${first.x.toFixed(1)},${baseY} Z`;
  }, [currentCoords, pad.top, plotH]);

  const activeIndex = hoveredIdx !== null && hoveredIdx < points.length ? hoveredIdx : null;
  const activePoint = activeIndex !== null ? points[activeIndex] : null;
  const activeCoord = activeIndex !== null ? currentCoords[activeIndex] : null;

  const formatVal = (v: number) => (valueKind === "vnd" ? formatVnd(v) : formatInt(v));

  return (
    <div className={styles.wrap} onMouseLeave={() => setHoveredIdx(null)}>
      <div className={styles.legendRow}>
        <span className={styles.legendItem}>
          <span className={styles.swatchCurrent} />
          <span>{currentLegend}</span>
        </span>
        {hasPrior && priorLegend && (
          <span className={styles.legendItem}>
            <span className={styles.swatchPrior} />
            <span>{priorLegend}</span>
          </span>
        )}
        {activePoint ? (
          <span className={styles.readout}>
            <strong>{activePoint.label}</strong>: {formatVal(activePoint.current)}
            {hasPrior && typeof activePoint.prior === "number"
              ? ` · Trước: ${formatVal(activePoint.prior)}`
              : ""}
          </span>
        ) : (
          <span className={styles.readoutHint}>Di chuột vào biểu đồ để xem chi tiết từng ngày</span>
        )}
      </div>

      <svg
        viewBox={`0 0 ${width} ${height}`}
        className={styles.svg}
        role="img"
        aria-label={ariaLabel}
      >
        <defs>
          <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--accent)" stopOpacity="0.14" />
            <stop offset="100%" stopColor="var(--accent)" stopOpacity="0" />
          </linearGradient>
        </defs>

        {yTicks.map((tick) => {
          const y = pad.top + plotH - (tick / yMax) * plotH;
          return (
            <g key={tick}>
              <line
                x1={pad.left}
                x2={width - pad.right}
                y1={y}
                y2={y}
                className={styles.gridLine}
              />
              <text x={pad.left - 8} y={y + 4} textAnchor="end" className={styles.axisLabel}>
                {formatAxisTick(tick, valueKind)}
              </text>
            </g>
          );
        })}

        {areaPath && <path d={areaPath} fill={`url(#${gradId})`} />}

        {hasPrior && priorCoords.length > 1 && (
          <polyline
            fill="none"
            points={toPolyline(priorCoords)}
            className={styles.priorLine}
            vectorEffect="non-scaling-stroke"
          />
        )}

        {currentCoords.length > 1 && (
          <polyline
            fill="none"
            points={toPolyline(currentCoords)}
            className={styles.currentLine}
            vectorEffect="non-scaling-stroke"
          />
        )}

        {activeCoord && (
          <g>
            <line
              x1={activeCoord.x}
              x2={activeCoord.x}
              y1={pad.top}
              y2={pad.top + plotH}
              className={styles.crosshair}
            />
            <circle cx={activeCoord.x} cy={activeCoord.y} r={4.5} className={styles.activeDot} />
          </g>
        )}

        {currentCoords.map((c, i) => {
          if (!xLabelIndices.has(i)) return null;
          return (
            <text
              key={`x-${points[i]?.label ?? i}`}
              x={c.x}
              y={height - 8}
              textAnchor={i === 0 ? "start" : i === points.length - 1 ? "end" : "middle"}
              className={styles.axisLabel}
            >
              {points[i]?.label ?? ""}
            </text>
          );
        })}

        {currentCoords.map((c, i) => {
          const sliceW = points.length > 1 ? plotW / (points.length - 1) : plotW;
          const x = Math.max(pad.left, c.x - sliceW / 2);
          const rectW = Math.min(width - pad.right - x, sliceW);
          return (
            <rect
              key={`hit-${i}`}
              x={x}
              y={pad.top}
              width={Math.max(4, rectW)}
              height={plotH}
              fill="transparent"
              onMouseEnter={() => setHoveredIdx(i)}
              onFocus={() => setHoveredIdx(i)}
              tabIndex={0}
              aria-label={`${points[i]?.label ?? ""}: ${formatVal(points[i]?.current ?? 0)}`}
            />
          );
        })}
      </svg>
    </div>
  );
}
