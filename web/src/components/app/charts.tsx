"use client";

import React, { useEffect, useId, useMemo, useRef, useState } from "react";
import { formatInt } from "@/lib/format";
import { cn } from "@/lib/utils";

function computeRoundTicks(minVal: number, maxVal: number, count = 4): number[] {
  const loBound = minVal;
  let hiBound = maxVal;
  if (hiBound <= loBound) {
    hiBound = loBound + 1;
  }
  const rawStep = (hiBound - loBound) / count;
  const mag = 10 ** Math.floor(Math.log10(rawStep));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= rawStep) ?? 10 * mag;
  const lo = Math.floor(loBound / step) * step;
  let hi = Math.ceil(hiBound / step) * step;
  if (hi <= hiBound) hi += step;
  const ticks: number[] = [];
  for (let v = lo; v <= hi + step * 0.01; v += step) {
    ticks.push(Number(v.toFixed(6)));
  }
  return ticks;
}

function useChartWidth(defaultWidth = 560) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [width, setWidth] = useState<number>(defaultWidth);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    const update = () => {
      const next = Math.max(220, Math.round(el.clientWidth || defaultWidth));
      setWidth(next);
    };
    update();

    let ro: ResizeObserver | null = null;
    if (typeof ResizeObserver !== "undefined") {
      ro = new ResizeObserver(update);
      ro.observe(el);
    } else {
      window.addEventListener("resize", update);
    }

    return () => {
      ro?.disconnect();
      window.removeEventListener("resize", update);
    };
  }, [defaultWidth]);

  return { ref, width };
}

export interface LineSeries {
  name: string;
  values: Array<number | null>;
  color?: string;
  dashed?: boolean;
}

export interface ChartAnnotation {
  index: number;
  label: string;
}

export interface LineChartProps {
  labels: string[];
  series: LineSeries[];
  height?: number;
  formatValue?: (v: number) => string;
  formatTick?: (v: number) => string;
  showArea?: boolean;
  annotations?: ChartAnnotation[];
  referenceLine?: { value: number; label?: string };
  ariaLabel?: string;
}

export function LineChart({
  labels,
  series,
  height = 228,
  formatValue = formatInt,
  formatTick,
  showArea = false,
  annotations,
  referenceLine,
  ariaLabel,
}: LineChartProps) {
  const { ref, width } = useChartWidth(560);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  const clipUid = useId().replace(/[^a-zA-Z0-9_-]/g, "");
  const plotClipId = `nm-ui-line-clip-${clipUid}`;

  const tickFormatter = formatTick ?? formatValue;

  const { yt, padL, padT, padB, padR, yScale, xScale, xIndices } = useMemo(() => {
    const allNums = series
      .flatMap((s) => s.values.filter((v): v is number => v != null))
      .concat(referenceLine ? [referenceLine.value] : []);
    const maxV = allNums.length ? Math.max(...allNums) : 10;
    const minV = allNums.length ? Math.min(0, ...allNums) : 0;
    const ticks = computeRoundTicks(minV, maxV, 4);
    const maxTickChars = Math.max(...ticks.map((v) => tickFormatter(v).length), 3);
    const leftPad = Math.max(46, maxTickChars * 7.6 + 14);
    const topPad = annotations && annotations.length > 0 ? 28 : 14;
    const bottomPad = 26;
    const rightPad = 12;
    const plotW = Math.max(80, width - leftPad - rightPad);
    const plotH = Math.max(64, height - topPad - bottomPad);
    const yLo = ticks[0];
    const yHi = ticks[ticks.length - 1];

    const xFn = (i: number) =>
      labels.length <= 1 ? leftPad : leftPad + (i / (labels.length - 1)) * plotW;
    const yFn = (v: number) => topPad + plotH * (1 - (v - yLo) / Math.max(1e-6, yHi - yLo));

    const maxLabels = Math.max(2, Math.min(7, Math.floor(plotW / 72)));
    const step = Math.max(1, Math.ceil(labels.length / maxLabels));
    const picks: number[] = [];
    for (let i = 0; i < labels.length; i += step) picks.push(i);

    return {
      yt: ticks,
      padL: leftPad,
      padT: topPad,
      padB: bottomPad,
      padR: rightPad,
      yScale: yFn,
      xScale: xFn,
      xIndices: picks,
    };
  }, [series, referenceLine, tickFormatter, annotations, width, height, labels.length]);

  const seriesColor = (sr: LineSeries, idx: number) =>
    sr.color ?? (idx === 0 ? "var(--ui-brand)" : "var(--ui-muted-foreground)");

  const handlePointerMove = (e: React.PointerEvent<SVGRectElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const relX = Math.max(0, Math.min(rect.width, e.clientX - rect.left));
    const ratio = rect.width > 0 ? relX / rect.width : 0;
    const idx = Math.round(ratio * Math.max(0, labels.length - 1));
    setHoverIdx(Math.max(0, Math.min(labels.length - 1, idx)));
  };

  const primarySeries = series[0];
  const primaryHoverVal = hoverIdx != null ? primarySeries?.values[hoverIdx] : null;
  const lastVal = primarySeries?.values.filter((v): v is number => v != null).at(-1);
  const computedAria =
    ariaLabel ??
    `${series.map((s) => s.name).join(", ")} từ ${labels[0] ?? ""} đến ${labels.at(-1) ?? ""}, giá trị gần nhất ${lastVal != null ? formatValue(lastVal) : "trống"}`;

  const baselineY = yScale(yt[0]);
  const plotW = Math.max(80, width - padL - padR);
  const plotH = Math.max(64, height - padT - padB);

  return (
    <div ref={ref} className="relative w-full select-none">
      {series.length > 1 ? (
        <ul className="mb-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
          {series.map((sr, idx) => (
            <li key={sr.name} className="inline-flex items-center gap-1.5">
              <span
                className="inline-block h-0.5 w-3.5 rounded-full"
                style={{ backgroundColor: seriesColor(sr, idx) }}
              />
              <span>{sr.name}</span>
            </li>
          ))}
        </ul>
      ) : null}

      <svg
        className="block w-full overflow-visible tabular-nums"
        viewBox={`0 0 ${width} ${height}`}
        height={height}
        role="img"
        aria-label={computedAria}
      >
        <title>{computedAria}</title>
        <defs>
          <clipPath id={plotClipId}>
            <rect x={padL - 2} y={padT - 6} width={plotW + 4} height={plotH + 10} />
          </clipPath>
        </defs>

        {yt.map((val, idx) => {
          const yy = yScale(val);
          const isBaseline = idx === 0;
          return (
            <g key={val}>
              <line
                x1={padL}
                x2={width - padR}
                y1={yy}
                y2={yy}
                stroke={isBaseline ? "var(--ui-input)" : "var(--ui-border)"}
                strokeWidth={1}
                strokeOpacity={isBaseline ? 0.55 : 1}
                shapeRendering="crispEdges"
              />
              <text
                x={padL - 8}
                y={yy + 4}
                textAnchor="end"
                fontSize={12}
                fill="var(--ui-muted-foreground)"
              >
                {tickFormatter(val)}
              </text>
            </g>
          );
        })}

        {xIndices.map((idx) => {
          const xx = xScale(idx);
          const anchor = idx === 0 ? "start" : idx === labels.length - 1 ? "end" : "middle";
          return (
            <g key={idx}>
              <line
                x1={xx}
                x2={xx}
                y1={baselineY}
                y2={baselineY + 4}
                stroke="var(--ui-border)"
                strokeWidth={1}
                shapeRendering="crispEdges"
              />
              <text
                x={xx}
                y={height - 6}
                textAnchor={anchor}
                fontSize={12}
                fill="var(--ui-muted-foreground)"
              >
                {labels[idx]}
              </text>
            </g>
          );
        })}

        {referenceLine ? (
          <g>
            <line
              x1={padL}
              x2={width - padR}
              y1={yScale(referenceLine.value)}
              y2={yScale(referenceLine.value)}
              stroke="var(--ui-muted-foreground)"
              strokeWidth={1}
              strokeDasharray="4 4"
              opacity={0.75}
            />
            {referenceLine.label ? (
              <text
                x={width - padR}
                y={yScale(referenceLine.value) - 6}
                textAnchor="end"
                fontSize={12}
                fill="var(--ui-muted-foreground)"
              >
                {referenceLine.label}
              </text>
            ) : null}
          </g>
        ) : null}

        {annotations?.map((ann) => {
          if (ann.index < 0 || ann.index >= labels.length) return null;
          const ax = xScale(ann.index);
          const alignEnd = ax > width * 0.72;
          return (
            <g key={`${ann.index}-${ann.label}`}>
              <line
                x1={ax}
                x2={ax}
                y1={padT - 6}
                y2={height - padB}
                stroke="var(--ui-muted-foreground)"
                strokeWidth={1}
                opacity={0.45}
                shapeRendering="crispEdges"
              />
              <text
                x={alignEnd ? ax - 4 : ax + 4}
                y={padT - 10}
                textAnchor={alignEnd ? "end" : "start"}
                fontSize={12}
                fill="var(--ui-muted-foreground)"
              >
                {ann.label}
              </text>
            </g>
          );
        })}

        <g clipPath={`url(#${plotClipId})`}>
          {series.map((sr, k) => {
            const color = seriesColor(sr, k);
            const pts: Array<[number, number] | null> = sr.values.map((v, i) =>
              v == null ? null : [xScale(i), yScale(v)]
            );
            let d = "";
            for (let i = 0; i < pts.length; i++) {
              const p = pts[i];
              if (!p) continue;
              const prev = i > 0 ? pts[i - 1] : null;
              d += `${prev ? "L" : "M"}${p[0].toFixed(1)} ${p[1].toFixed(1)}`;
            }
            const validPts = pts.filter((p): p is [number, number] => p != null);
            const areaD =
              showArea && k === 0 && validPts.length > 1
                ? `${d}L${validPts[validPts.length - 1][0].toFixed(1)} ${baselineY.toFixed(1)}L${validPts[0][0].toFixed(1)} ${baselineY.toFixed(1)}Z`
                : null;

            return (
              <g key={sr.name}>
                {areaD ? <path d={areaD} fill={color} fillOpacity={0.08} /> : null}
                <path
                  d={d}
                  fill="none"
                  stroke={color}
                  strokeWidth={2}
                  strokeLinejoin="round"
                  strokeLinecap="round"
                  strokeDasharray={sr.dashed ? "5 4" : undefined}
                />
              </g>
            );
          })}
        </g>

        {hoverIdx != null ? (
          <g>
            <line
              x1={xScale(hoverIdx)}
              x2={xScale(hoverIdx)}
              y1={padT}
              y2={baselineY}
              stroke="var(--ui-muted-foreground)"
              strokeWidth={1}
              strokeDasharray="3 3"
              opacity={0.7}
            />
            {primaryHoverVal != null ? (
              <line
                x1={padL}
                x2={xScale(hoverIdx)}
                y1={yScale(primaryHoverVal)}
                y2={yScale(primaryHoverVal)}
                stroke="var(--ui-muted-foreground)"
                strokeWidth={1}
                strokeDasharray="3 3"
                opacity={0.45}
              />
            ) : null}
            {series.map((sr, k) => {
              const val = sr.values[hoverIdx];
              if (val == null) return null;
              return (
                <circle
                  key={sr.name}
                  cx={xScale(hoverIdx)}
                  cy={yScale(val)}
                  r={4.5}
                  fill="var(--ui-card)"
                  stroke={seriesColor(sr, k)}
                  strokeWidth={2}
                />
              );
            })}
          </g>
        ) : null}

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

      {hoverIdx != null ? (
        <div
          className="pointer-events-none absolute top-2 z-10 min-w-[120px] rounded-lg border border-border bg-popover px-2.5 py-2 text-xs text-popover-foreground shadow-overlay tabular-nums"
          style={{
            left: `${Math.min(Math.max(8, xScale(hoverIdx) + 12), Math.max(8, width - 170))}px`,
          }}
        >
          <div className="mb-1 text-[11px] font-medium text-muted-foreground">
            {labels[hoverIdx]}
          </div>
          {series.map((sr) => {
            const val = sr.values[hoverIdx];
            return (
              <div key={sr.name} className="flex items-baseline justify-between gap-3">
                <span className="font-semibold text-foreground">
                  {val == null ? "—" : formatValue(val)}
                </span>
                {series.length > 1 ? (
                  <span className="text-muted-foreground">{sr.name}</span>
                ) : null}
              </div>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

export interface BarChartProps {
  labels: string[];
  values: number[];
  height?: number;
  highlightIndex?: number | number[] | null;
  formatValue?: (v: number) => string;
  formatTick?: (v: number) => string;
  ariaLabel?: string;
}

export function BarChart({
  labels,
  values,
  height = 208,
  highlightIndex = null,
  formatValue = formatInt,
  formatTick,
  ariaLabel,
}: BarChartProps) {
  const { ref, width } = useChartWidth(520);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  const tickFormatter = formatTick ?? formatValue;

  const isHighlighted = (idx: number) => {
    if (highlightIndex == null) return true;
    return Array.isArray(highlightIndex) ? highlightIndex.includes(idx) : highlightIndex === idx;
  };

  const { yt, padL, padT, padB, padR, yScale, bandWidth, barWidth, barX } = useMemo(() => {
    const maxV = values.length ? Math.max(...values, 1) : 10;
    const ticks = computeRoundTicks(0, maxV, 4);
    const maxTickChars = Math.max(...ticks.map((v) => tickFormatter(v).length), 3);
    const leftPad = Math.max(46, maxTickChars * 7.6 + 14);
    const topPad = 14;
    const bottomPad = 26;
    const rightPad = 10;
    const plotW = Math.max(80, width - leftPad - rightPad);
    const plotH = Math.max(64, height - topPad - bottomPad);
    const yHi = ticks[ticks.length - 1];
    const bwBand = plotW / Math.max(1, values.length);
    const bw = Math.max(10, Math.min(44, bwBand * 0.56));

    return {
      yt: ticks,
      padL: leftPad,
      padT: topPad,
      padB: bottomPad,
      padR: rightPad,
      yScale: (v: number) => topPad + plotH * (1 - v / Math.max(1e-6, yHi)),
      bandWidth: bwBand,
      barWidth: bw,
      barX: (i: number) => leftPad + i * bwBand + bwBand / 2,
    };
  }, [values, tickFormatter, width, height]);

  const zeroY = yScale(0);
  const activeHoverIdx =
    hoverIdx != null && hoverIdx >= 0 && hoverIdx < values.length ? hoverIdx : null;
  const computedAria = ariaLabel ?? `Biểu đồ cột gồm ${labels.length} kỳ`;

  return (
    <div ref={ref} className="relative w-full select-none">
      <svg
        className="block w-full overflow-visible tabular-nums"
        viewBox={`0 0 ${width} ${height}`}
        height={height}
        role="img"
        aria-label={computedAria}
      >
        <title>{computedAria}</title>
        {yt.map((val, idx) => {
          const yy = yScale(val);
          return (
            <g key={val}>
              <line
                x1={padL}
                x2={width - padR}
                y1={yy}
                y2={yy}
                stroke={idx === 0 ? "var(--ui-input)" : "var(--ui-border)"}
                strokeWidth={1}
                strokeOpacity={idx === 0 ? 0.55 : 1}
                shapeRendering="crispEdges"
              />
              <text
                x={padL - 8}
                y={yy + 4}
                textAnchor="end"
                fontSize={12}
                fill="var(--ui-muted-foreground)"
              >
                {tickFormatter(val)}
              </text>
            </g>
          );
        })}

        {activeHoverIdx != null ? (
          <line
            x1={padL}
            x2={width - padR}
            y1={yScale(values[activeHoverIdx])}
            y2={yScale(values[activeHoverIdx])}
            stroke="var(--ui-muted-foreground)"
            strokeWidth={1}
            strokeDasharray="3 3"
            opacity={0.5}
          />
        ) : null}

        {values.map((v, i) => {
          const cx = barX(i);
          const barTop = yScale(v);
          const h = Math.max(0, zeroY - barTop);
          const active = isHighlighted(i);
          return (
            <g
              key={labels[i] ?? i}
              onPointerEnter={() => setHoverIdx(i)}
              onPointerLeave={() => setHoverIdx(null)}
            >
              <rect
                x={cx - bandWidth / 2}
                y={padT}
                width={bandWidth}
                height={height - padT - padB}
                fill="transparent"
              />
              <rect
                x={cx - barWidth / 2}
                y={barTop}
                width={barWidth}
                height={h}
                rx={3}
                fill={active ? "var(--ui-brand)" : "var(--ui-muted-foreground)"}
                fillOpacity={
                  activeHoverIdx != null && activeHoverIdx !== i
                    ? active
                      ? 0.55
                      : 0.25
                    : active
                      ? 1
                      : 0.4
                }
                className={cn("transition-opacity duration-150")}
              >
                <title>{`${labels[i]}: ${formatValue(v)}`}</title>
              </rect>
              <text
                x={cx}
                y={height - 6}
                textAnchor="middle"
                fontSize={12}
                fill="var(--ui-muted-foreground)"
              >
                {labels[i]}
              </text>
            </g>
          );
        })}
      </svg>

      {activeHoverIdx != null ? (
        <div
          className="pointer-events-none absolute top-2 z-10 min-w-[110px] rounded-lg border border-border bg-popover px-2.5 py-2 text-xs text-popover-foreground shadow-overlay tabular-nums"
          style={{
            left: `${Math.min(Math.max(8, barX(activeHoverIdx) + 12), Math.max(8, width - 160))}px`,
          }}
        >
          <div className="mb-0.5 text-[11px] font-medium text-muted-foreground">
            {labels[activeHoverIdx]}
          </div>
          <div className="font-semibold text-foreground">{formatValue(values[activeHoverIdx])}</div>
        </div>
      ) : null}
    </div>
  );
}
