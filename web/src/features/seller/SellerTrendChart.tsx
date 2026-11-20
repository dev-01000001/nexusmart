"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import { formatInt } from "@/lib/format";
import styles from "./SellerTrendChart.module.css";

function computeRoundTicks(maxVal: number, count = 4): number[] {
  const hiBound = maxVal <= 0 ? 10 : maxVal;
  const rawStep = hiBound / count;
  const mag = 10 ** Math.floor(Math.log10(rawStep));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= rawStep) ?? 10 * mag;
  let hi = Math.ceil(hiBound / step) * step;
  if (hi <= hiBound) hi += step;
  const ticks: number[] = [];
  for (let v = 0; v <= hi + step * 0.01; v += step) {
    ticks.push(Number(v.toFixed(6)));
  }
  return ticks;
}

function useMeasureWidth(fallback = 680) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [width, setWidth] = useState(fallback);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const sync = () => setWidth(Math.max(240, Math.round(el.clientWidth || fallback)));
    sync();
    if (typeof ResizeObserver !== "undefined") {
      const ro = new ResizeObserver(sync);
      ro.observe(el);
      return () => ro.disconnect();
    }
    window.addEventListener("resize", sync);
    return () => window.removeEventListener("resize", sync);
  }, [fallback]);

  return { ref, width };
}

export interface SellerTrendChartProps {
  labels: string[];
  values: number[];
  metricName: string;
  formatValue?: (v: number) => string;
  formatTick?: (v: number) => string;
  annotation?: { index: number; label: string } | null;
  desktopHeight?: number;
  mobileHeight?: number;
}

/**
 * Achromatic Ledger trend line chart (seller-sheet.md §5):
 * - Single data hue (#0d7480 / var(--chart-1)) on the active segment
 * - Straight segments, 2.25px stroke, no fill, 3-4 solid 1px horizontal gridlines, round y-ticks
 * - Interactive 8px hover dot + 1px dashed guide + muted pale-grey segment after the hovered point
 * - Responsive phone height (180px) with 4 evenly spaced x-axis labels
 */
export function SellerTrendChart({
  labels,
  values,
  metricName,
  formatValue = formatInt,
  formatTick,
  annotation = null,
  desktopHeight = 204,
  mobileHeight = 180,
}: SellerTrendChartProps) {
  const { ref, width } = useMeasureWidth(720);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  const isCompact = width < 480;
  const height = isCompact ? mobileHeight : desktopHeight;
  const tickFormatter = formatTick ?? formatValue;

  const { yt, padL, padT, padB, padR, xScale, yScale, xIndices } = useMemo(() => {
    const maxV = values.length ? Math.max(...values, 0) : 0;
    const ticks = computeRoundTicks(maxV, 3);
    const maxTickChars = Math.max(...ticks.map((t) => tickFormatter(t).length), 3);
    const leftPad = Math.max(48, Math.round(maxTickChars * 7.1 + 14));
    const topPad = annotation ? 26 : 12;
    const bottomPad = 26;
    const rightPad = 14;
    const plotW = Math.max(80, width - leftPad - rightPad);
    const plotH = Math.max(60, height - topPad - bottomPad);
    const yHi = ticks[ticks.length - 1] || 1;

    const xFn = (i: number) =>
      labels.length <= 1 ? leftPad : leftPad + (i / (labels.length - 1)) * plotW;
    const yFn = (v: number) => topPad + plotH * (1 - v / yHi);

    // Even step on x-labels (4 labels on phone, up to 7 on desktop)
    const n = labels.length;
    const picks: number[] = [];
    if (n <= 4) {
      for (let i = 0; i < n; i++) picks.push(i);
    } else if (isCompact) {
      // Exact 4 evenly spaced labels for 7-day series: 0, 2, 4, 6
      const step = Math.max(1, Math.floor((n - 1) / 3));
      for (let i = 0; i < n; i += step) {
        if (picks.length < 4) picks.push(i);
      }
    } else {
      for (let i = 0; i < n; i++) picks.push(i);
    }

    return {
      yt: ticks,
      padL: leftPad,
      padT: topPad,
      padB: bottomPad,
      padR: rightPad,
      xScale: xFn,
      yScale: yFn,
      xIndices: picks,
    };
  }, [values, labels.length, tickFormatter, annotation, width, height, isCompact]);

  const points = useMemo(
    () => values.map((v, i) => [xScale(i), yScale(v)] as const),
    [values, xScale, yScale]
  );

  const splitAt = hoverIdx ?? points.length - 1;
  const activePath = useMemo(() => {
    const slice = points.slice(0, splitAt + 1);
    return slice
      .map((p, i) => `${i === 0 ? "M" : "L"}${p[0].toFixed(1)} ${p[1].toFixed(1)}`)
      .join("");
  }, [points, splitAt]);

  const mutedTailPath = useMemo(() => {
    if (hoverIdx == null || hoverIdx >= points.length - 1) return "";
    const slice = points.slice(hoverIdx);
    return slice
      .map((p, i) => `${i === 0 ? "M" : "L"}${p[0].toFixed(1)} ${p[1].toFixed(1)}`)
      .join("");
  }, [points, hoverIdx]);

  const handlePointerMove = (e: React.PointerEvent<SVGRectElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const relX = Math.max(0, Math.min(rect.width, e.clientX - rect.left));
    const ratio = rect.width > 0 ? relX / rect.width : 0;
    const idx = Math.round(ratio * Math.max(0, labels.length - 1));
    setHoverIdx(Math.max(0, Math.min(labels.length - 1, idx)));
  };

  const latestVal = values.at(-1) ?? 0;

  return (
    <div className={styles.chartBox} ref={ref}>
      <svg
        className={styles.svg}
        viewBox={`0 0 ${width} ${height}`}
        height={height}
        role="img"
        aria-label={`${metricName} từ ${labels[0] ?? ""} đến ${labels.at(-1) ?? ""}, ngày gần nhất ${formatValue(latestVal)}`}
      >
        {/* Solid 1px horizontal gridlines + round y-axis ticks (no y-axis line) */}
        {yt.map((tick) => {
          const yy = yScale(tick);
          return (
            <g key={tick}>
              <line
                x1={padL}
                x2={width - padR}
                y1={yy}
                y2={yy}
                stroke="var(--grid)"
                strokeWidth={1}
                shapeRendering="crispEdges"
              />
              <text x={padL - 8} y={yy + 4} textAnchor="end" fontSize={12} fill="var(--axis-ink)">
                {tickFormatter(tick)}
              </text>
            </g>
          );
        })}

        {/* X-axis labels */}
        {xIndices.map((idx) => {
          const xx = xScale(idx);
          const anchor = idx === 0 ? "start" : idx === labels.length - 1 ? "end" : "middle";
          return (
            <text
              key={idx}
              x={xx}
              y={height - 6}
              textAnchor={anchor}
              fontSize={12}
              fill="var(--axis-ink)"
            >
              {labels[idx]}
            </text>
          );
        })}

        {/* Event annotation above plot (e.g. Flash sale 12:00 spike) */}
        {annotation && annotation.index >= 0 && annotation.index < labels.length && (
          <g>
            <line
              x1={xScale(annotation.index)}
              x2={xScale(annotation.index)}
              y1={padT - 5}
              y2={height - padB}
              stroke="var(--axis-ink)"
              strokeWidth={1}
              opacity={0.35}
              shapeRendering="crispEdges"
            />
            <text
              x={
                xScale(annotation.index) > width * 0.68
                  ? xScale(annotation.index) - 5
                  : xScale(annotation.index) + 5
              }
              y={padT - 9}
              textAnchor={xScale(annotation.index) > width * 0.68 ? "end" : "start"}
              fontSize={12}
              fill="var(--axis-ink)"
            >
              {annotation.label}
            </text>
          </g>
        )}

        {/* Muted segment after hovered cursor */}
        {mutedTailPath && (
          <path
            d={mutedTailPath}
            fill="none"
            stroke="var(--chart-muted)"
            strokeWidth={2.25}
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        )}

        {/* Active data series in the one brand data hue (#0d7480) */}
        {activePath && (
          <path
            d={activePath}
            fill="none"
            stroke="var(--chart-1)"
            strokeWidth={2.25}
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        )}

        {/* Hover vertical dashed guide + 8px hover ring dot */}
        {hoverIdx != null && points[hoverIdx] && (
          <g>
            <line
              x1={points[hoverIdx][0]}
              x2={points[hoverIdx][0]}
              y1={padT}
              y2={height - padB}
              stroke="var(--axis-ink)"
              strokeWidth={1}
              strokeDasharray="3 3"
              opacity={0.6}
            />
            <circle
              cx={points[hoverIdx][0]}
              cy={points[hoverIdx][1]}
              r={4}
              fill="var(--card)"
              stroke="var(--chart-1)"
              strokeWidth={2.25}
            />
          </g>
        )}

        {/* Pointer capture surface */}
        <rect
          x={padL}
          y={padT}
          width={Math.max(1, width - padL - padR)}
          height={Math.max(1, height - padT - padB)}
          fill="transparent"
          onPointerMove={handlePointerMove}
          onPointerLeave={() => setHoverIdx(null)}
        />
      </svg>

      {hoverIdx != null && (
        <div
          className={styles.tooltip}
          style={{
            left: `${Math.min(Math.max(padL, xScale(hoverIdx) + 10), Math.max(padL, width - 176))}px`,
          }}
        >
          <div className={styles.tooltipRow}>
            <span className={styles.tooltipLabel}>{labels[hoverIdx]}</span>
            <span className={styles.tooltipValue}>{formatValue(values[hoverIdx] ?? 0)}</span>
          </div>
        </div>
      )}
    </div>
  );
}
