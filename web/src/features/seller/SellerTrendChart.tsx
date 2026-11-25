"use client";

import React, { useEffect, useId, useMemo, useRef, useState } from "react";
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
    const sync = () => {
      const rectW = Math.floor(el.getBoundingClientRect().width || el.clientWidth || 0);
      if (rectW > 0) setWidth(Math.max(220, rectW));
    };
    sync();
    if (typeof ResizeObserver !== "undefined") {
      const ro = new ResizeObserver(sync);
      ro.observe(el);
      return () => ro.disconnect();
    }
    window.addEventListener("resize", sync);
    return () => window.removeEventListener("resize", sync);
  }, []);

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
 * Framed-sheet trend line chart:
 * - Single data hue (--chart-1 / #0d7480) on the active segment, straight segments, 2.25px stroke
 * - Axis tick labels >= 13px (--fs-small floor), tabular figures
 * - Synchronized one-shot horizontal clip reveal on series switch
 * - Interactive 8px hover dot + 1px dashed vertical guide + pale-grey trailing segment
 */
export function SellerTrendChart({
  labels,
  values,
  metricName,
  formatValue = formatInt,
  formatTick,
  annotation = null,
  desktopHeight = 216,
  mobileHeight = 180,
}: SellerTrendChartProps) {
  const { ref, width } = useMeasureWidth(720);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  const clipUid = useId().replace(/[^a-zA-Z0-9_-]/g, "");
  const clipId = `nm-seller-trend-${clipUid}`;
  const isCompact = width < 480;
  const height = isCompact ? mobileHeight : desktopHeight;
  const tickFormatter = formatTick ?? formatValue;

  const { yt, padL, padT, padB, padR, plotW, plotH, xScale, yScale, xIndices } = useMemo(() => {
    const maxV = values.length ? Math.max(...values, 0) : 0;
    const ticks = computeRoundTicks(maxV, 3);
    const maxTickChars = Math.max(...ticks.map((t) => tickFormatter(t).length), 3);
    const leftPad = Math.max(54, Math.ceil(maxTickChars * 8.2 + 12));
    const topPad = annotation ? 28 : 14;
    const bottomPad = 28;
    const rightPad = isCompact ? 8 : 14;
    const pW = Math.max(80, width - leftPad - rightPad);
    const pH = Math.max(60, height - topPad - bottomPad);
    const yHi = ticks[ticks.length - 1] || 1;

    const xFn = (i: number) =>
      labels.length <= 1 ? leftPad : leftPad + (i / (labels.length - 1)) * pW;
    const yFn = (v: number) => topPad + pH * (1 - v / yHi);

    const n = labels.length;
    const picks: number[] = [];
    if (n <= 4) {
      for (let i = 0; i < n; i++) picks.push(i);
    } else if (isCompact) {
      const raw = [0, Math.round((n - 1) / 3), Math.round((2 * (n - 1)) / 3), n - 1];
      for (const idx of raw) {
        if (!picks.includes(idx)) picks.push(idx);
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
      plotW: pW,
      plotH: pH,
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
        <defs>
          <clipPath id={clipId}>
            <rect
              key={metricName}
              x={padL - 4}
              y={padT - 8}
              width={plotW + 8}
              height={plotH + 16}
              className={styles.plotReveal}
              style={{ transformOrigin: `${padL - 4}px 0px` }}
            />
          </clipPath>
        </defs>

        {yt.map((tick, idx) => {
          const yy = yScale(tick);
          return (
            <g key={tick}>
              <line
                x1={padL}
                x2={width - padR}
                y1={yy}
                y2={yy}
                stroke={idx === 0 ? "var(--control)" : "var(--grid)"}
                strokeWidth={1}
                shapeRendering="crispEdges"
              />
              <text x={padL - 8} y={yy + 4} textAnchor="end" fontSize={13} fill="var(--axis-ink)">
                {tickFormatter(tick)}
              </text>
            </g>
          );
        })}

        {xIndices.map((idx) => {
          const xx = xScale(idx);
          const anchor = idx === 0 ? "start" : idx === labels.length - 1 ? "end" : "middle";
          return (
            <text
              key={idx}
              x={xx}
              y={height - 6}
              textAnchor={anchor}
              fontSize={13}
              fill="var(--axis-ink)"
            >
              {labels[idx]}
            </text>
          );
        })}

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
              fontSize={13}
              fill="var(--axis-ink)"
            >
              {annotation.label}
            </text>
          </g>
        )}

        <g clipPath={`url(#${clipId})`}>
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
        </g>

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
              r={4.5}
              fill="var(--bg-0)"
              stroke="var(--chart-1)"
              strokeWidth={2.25}
            />
          </g>
        )}

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
            left: `${Math.min(Math.max(padL, xScale(hoverIdx) + 10), Math.max(padL, width - 190))}px`,
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
