"use client";

import React, { useEffect, useId, useMemo, useRef, useState } from "react";
import { formatInt } from "@/lib/format";
import styles from "./SvgChart.module.css";

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

function useChartLifecycle(defaultWidth = 560) {
  const ref = useRef<HTMLDivElement | null>(null);
  const [width, setWidth] = useState<number>(defaultWidth);
  const [entered, setEntered] = useState<boolean>(false);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    const update = () => {
      const next = Math.max(240, Math.round(el.clientWidth || defaultWidth));
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

    const reduced =
      typeof window.matchMedia === "function" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    if (reduced || typeof IntersectionObserver === "undefined") {
      setEntered(true);
      return () => {
        ro?.disconnect();
        window.removeEventListener("resize", update);
      };
    }

    const rect = el.getBoundingClientRect();
    if (rect.top < (window.innerHeight || 900) * 0.96) {
      setEntered(true);
      return () => {
        ro?.disconnect();
        window.removeEventListener("resize", update);
      };
    }

    const io = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setEntered(true);
            io.disconnect();
            break;
          }
        }
      },
      { rootMargin: "0px 0px -4% 0px", threshold: 0.08 }
    );
    io.observe(el);

    return () => {
      io.disconnect();
      ro?.disconnect();
      window.removeEventListener("resize", update);
    };
  }, [defaultWidth]);

  return { ref, width, entered };
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
  const { ref, width, entered } = useChartLifecycle(640);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  const clipUid = useId().replace(/[^a-zA-Z0-9_-]/g, "");
  const plotClipId = `nm-line-clip-${clipUid}`;

  const tickFormatter = formatTick ?? formatValue;

  const { yt, padL, padT, padB, padR, yScale, xScale, xIndices } = useMemo(() => {
    const allNums = series
      .flatMap((s) => s.values.filter((v): v is number => v != null))
      .concat(referenceLine ? [referenceLine.value] : []);
    const maxV = allNums.length ? Math.max(...allNums) : 10;
    const minV = allNums.length ? Math.min(0, ...allNums) : 0;
    const ticks = computeRoundTicks(minV, maxV, 4);
    const maxTickChars = Math.max(...ticks.map((v) => tickFormatter(v).length), 3);
    const leftPad = Math.max(48, maxTickChars * 7.8 + 16);
    const topPad = annotations && annotations.length > 0 ? 28 : 14;
    const bottomPad = 28;
    const rightPad = 14;
    const plotW = Math.max(80, width - leftPad - rightPad);
    const plotH = Math.max(64, height - topPad - bottomPad);
    const yLo = ticks[0];
    const yHi = ticks[ticks.length - 1];

    const xFn = (i: number) =>
      labels.length <= 1 ? leftPad : leftPad + (i / (labels.length - 1)) * plotW;
    const yFn = (v: number) => topPad + plotH * (1 - (v - yLo) / Math.max(1e-6, yHi - yLo));

    const maxLabels = Math.max(2, Math.min(7, Math.floor(plotW / 76)));
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
    sr.color ??
    (idx === 0 ? "var(--chart-1)" : idx === 1 ? "var(--chart-muted)" : "var(--chart-2)");

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
    <div className={`${styles.chartBox} ${entered ? styles.entered : ""}`} ref={ref}>
      {series.length > 1 && (
        <ul className={styles.legend}>
          {series.map((sr, idx) => (
            <li key={sr.name} className={styles.legendItem}>
              <span
                className={styles.legendLine}
                style={{ backgroundColor: seriesColor(sr, idx) }}
              />
              <span>{sr.name}</span>
            </li>
          ))}
        </ul>
      )}

      <svg
        className={styles.svg}
        viewBox={`0 0 ${width} ${height}`}
        height={height}
        role="img"
        aria-label={computedAria}
      >
        <defs>
          <clipPath id={plotClipId}>
            <rect
              x={padL - 2}
              y={padT - 6}
              width={plotW + 4}
              height={plotH + 10}
              className={styles.plotRevealRect}
              style={{ transformOrigin: `${padL - 2}px 0px` }}
            />
          </clipPath>
        </defs>
        {/* Horizontal gridlines, baseline axis rule, and y ticks (>= 13px) */}
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
                stroke={isBaseline ? "var(--control)" : "var(--grid)"}
                strokeWidth={1}
                shapeRendering="crispEdges"
              />
              <text x={padL - 8} y={yy + 4} textAnchor="end" fontSize={13} fill="var(--axis-ink)">
                {tickFormatter(val)}
              </text>
            </g>
          );
        })}

        {/* X axis ticks and labels (>= 13px) */}
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
                stroke="var(--control)"
                strokeWidth={1}
                shapeRendering="crispEdges"
              />
              <text x={xx} y={height - 6} textAnchor={anchor} fontSize={13} fill="var(--axis-ink)">
                {labels[idx]}
              </text>
            </g>
          );
        })}

        {/* Optional reference line */}
        {referenceLine && (
          <g>
            <line
              x1={padL}
              x2={width - padR}
              y1={yScale(referenceLine.value)}
              y2={yScale(referenceLine.value)}
              stroke="var(--axis-ink)"
              strokeWidth={1}
              strokeDasharray="4 4"
              opacity={0.75}
            />
            {referenceLine.label && (
              <text
                x={width - padR}
                y={yScale(referenceLine.value) - 6}
                textAnchor="end"
                fontSize={13}
                fill="var(--axis-ink)"
              >
                {referenceLine.label}
              </text>
            )}
          </g>
        )}

        {/* Event annotations above plot */}
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
                stroke="var(--axis-ink)"
                strokeWidth={1}
                opacity={0.45}
                shapeRendering="crispEdges"
              />
              <text
                x={alignEnd ? ax - 4 : ax + 4}
                y={padT - 10}
                textAnchor={alignEnd ? "end" : "start"}
                fontSize={13}
                fill="var(--axis-ink)"
              >
                {ann.label}
              </text>
            </g>
          );
        })}

        {/* Series paths + optional area share one synchronized horizontal reveal clipPath */}
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
                {areaD && <path d={areaD} fill={color} className={styles.areaPath} />}
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

        {/* Hover vertical + horizontal alignment crosshairs and active markers */}
        {hoverIdx != null && (
          <g className={styles.guideGroup}>
            <line
              x1={xScale(hoverIdx)}
              x2={xScale(hoverIdx)}
              y1={padT}
              y2={baselineY}
              stroke="var(--ink-3)"
              strokeWidth={1}
              strokeDasharray="3 3"
              opacity={0.7}
            />
            {primaryHoverVal != null && (
              <line
                x1={padL}
                x2={xScale(hoverIdx)}
                y1={yScale(primaryHoverVal)}
                y2={yScale(primaryHoverVal)}
                stroke="var(--ink-3)"
                strokeWidth={1}
                strokeDasharray="3 3"
                opacity={0.45}
              />
            )}
            {series.map((sr, k) => {
              const val = sr.values[hoverIdx];
              if (val == null) return null;
              return (
                <circle
                  key={sr.name}
                  cx={xScale(hoverIdx)}
                  cy={yScale(val)}
                  r={4.5}
                  fill="var(--card)"
                  stroke={seriesColor(sr, k)}
                  strokeWidth={2}
                />
              );
            })}
          </g>
        )}

        {/* Transparent pointer capture area */}
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
            left: `${Math.min(Math.max(8, xScale(hoverIdx) + 12), Math.max(8, width - 180))}px`,
          }}
        >
          <div className={styles.tooltipLabel}>{labels[hoverIdx]}</div>
          {series.map((sr) => {
            const val = sr.values[hoverIdx];
            return (
              <div key={sr.name} className={styles.tooltipRow}>
                <span className={styles.tooltipValue}>{val == null ? "-" : formatValue(val)}</span>
                {series.length > 1 && <span>{sr.name}</span>}
              </div>
            );
          })}
        </div>
      )}
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
  const { ref, width, entered } = useChartLifecycle(560);
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
    const leftPad = Math.max(48, maxTickChars * 7.8 + 16);
    const topPad = 14;
    const bottomPad = 28;
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

  return (
    <div className={`${styles.chartBox} ${entered ? styles.entered : ""}`} ref={ref}>
      <svg
        className={styles.svg}
        viewBox={`0 0 ${width} ${height}`}
        height={height}
        role="img"
        aria-label={ariaLabel ?? `Biểu đồ cột gồm ${labels.length} kỳ`}
      >
        {yt.map((val, idx) => {
          const yy = yScale(val);
          return (
            <g key={val}>
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
                {tickFormatter(val)}
              </text>
            </g>
          );
        })}

        {/* Hover alignment guide */}
        {activeHoverIdx != null && (
          <line
            x1={padL}
            x2={width - padR}
            y1={yScale(values[activeHoverIdx])}
            y2={yScale(values[activeHoverIdx])}
            stroke="var(--ink-3)"
            strokeWidth={1}
            strokeDasharray="3 3"
            opacity={0.5}
          />
        )}

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
                fill={active ? "var(--chart-1)" : "var(--chart-muted)"}
                opacity={activeHoverIdx != null && activeHoverIdx !== i ? 0.55 : 1}
                shapeRendering="crispEdges"
                className={styles.barRect}
                style={
                  {
                    "--bar-idx": i,
                    transformOrigin: `${cx}px ${zeroY}px`,
                  } as React.CSSProperties
                }
              >
                <title>{`${labels[i]}: ${formatValue(v)}`}</title>
              </rect>
              <text x={cx} y={height - 6} textAnchor="middle" fontSize={13} fill="var(--axis-ink)">
                {labels[i]}
              </text>
            </g>
          );
        })}
      </svg>

      {activeHoverIdx != null && (
        <div
          className={styles.tooltip}
          style={{
            left: `${Math.min(Math.max(8, barX(activeHoverIdx) + 12), Math.max(8, width - 170))}px`,
          }}
        >
          <div className={styles.tooltipLabel}>{labels[activeHoverIdx]}</div>
          <div className={styles.tooltipValue}>{formatValue(values[activeHoverIdx])}</div>
        </div>
      )}
    </div>
  );
}

export interface BarListRow {
  label: string;
  subLabel?: string;
  value: number;
}

export interface BarListProps {
  rows: BarListRow[];
  formatValue?: (v: number) => string;
  accentTop?: boolean;
}

export function BarList({ rows, formatValue = formatInt, accentTop = true }: BarListProps) {
  const { ref, entered } = useChartLifecycle(420);
  const maxVal = Math.max(...rows.map((r) => r.value), 1);

  return (
    <div ref={ref} className={entered ? styles.entered : ""}>
      <ol className={styles.barList}>
        {rows.map((row, idx) => {
          const ratio = Math.max(0, Math.min(1, row.value / maxVal));
          return (
            <li key={row.label} className={styles.barListItem}>
              <span className={styles.barListLabel}>
                {row.label}
                {row.subLabel && <span className={styles.barListSub}>{row.subLabel}</span>}
              </span>
              <span className={styles.barListValue}>{formatValue(row.value)}</span>
              <span className={styles.barListTrack} aria-hidden="true">
                <span
                  className={styles.barListFill}
                  style={
                    {
                      "--ratio": ratio,
                      "--bar-idx": idx,
                      backgroundColor:
                        idx === 0 && accentTop ? "var(--chart-1)" : "var(--chart-muted)",
                    } as React.CSSProperties
                  }
                />
              </span>
            </li>
          );
        })}
      </ol>
    </div>
  );
}

export interface ProgressBarProps {
  value: number;
  max: number;
  leftLabel?: string;
  rightLabel?: string;
  tone?: "accent" | "ink" | "negative";
}

export function ProgressBar({
  value,
  max,
  leftLabel,
  rightLabel,
  tone = "accent",
}: ProgressBarProps) {
  const ratio = max > 0 ? Math.max(0, Math.min(1, value / max)) : 0;
  const fillCol =
    tone === "negative" ? "var(--negative)" : tone === "ink" ? "var(--ink)" : "var(--chart-1)";

  return (
    <div className={styles.progressWrap}>
      {(leftLabel || rightLabel) && (
        <div className={styles.progressHeader}>
          <span className={styles.progressLeft}>{leftLabel}</span>
          <span className={styles.progressRight}>{rightLabel}</span>
        </div>
      )}
      <div
        className={styles.progressTrack}
        role="progressbar"
        aria-valuenow={value}
        aria-valuemin={0}
        aria-valuemax={max}
      >
        {ratio > 0 && (
          <div
            className={styles.progressFill}
            style={{
              transform: `scaleX(${ratio.toFixed(4)})`,
              backgroundColor: fillCol,
            }}
          />
        )}
      </div>
    </div>
  );
}

export interface RingChartProps {
  ratio: number; // 0..1
  label: string;
  subLabel?: string;
  size?: number;
  strokeWidth?: number;
  color?: string;
}

export function RingChart({
  ratio,
  label,
  subLabel,
  size = 116,
  strokeWidth = 6,
  color = "var(--chart-1)",
}: RingChartProps) {
  const { ref, entered } = useChartLifecycle(size);
  const clamped = Math.max(0, Math.min(1, ratio));
  const radius = (size - strokeWidth) / 2;
  const circumference = 2 * Math.PI * radius;

  return (
    <div
      ref={ref}
      className={`${styles.ringWrap} ${entered ? styles.entered : ""}`}
      style={{ width: size, height: size }}
    >
      <svg
        width={size}
        height={size}
        viewBox={`0 0 ${size} ${size}`}
        role="img"
        aria-label={`${label}${subLabel ? ` ${subLabel}` : ""}`}
      >
        <circle
          cx={size / 2}
          cy={size / 2}
          r={radius}
          fill="none"
          stroke="var(--grid)"
          strokeWidth={strokeWidth}
        />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={radius}
          fill="none"
          stroke={color}
          strokeWidth={strokeWidth}
          strokeLinecap="round"
          strokeDasharray={circumference}
          strokeDashoffset={circumference * (1 - clamped)}
          transform={`rotate(-90 ${size / 2} ${size / 2})`}
          className={styles.ringArc}
          style={{ "--ring-c": circumference } as React.CSSProperties}
        />
      </svg>
      <div className={styles.ringCenter}>
        <span
          className={styles.ringValue}
          style={{ fontSize: `${Math.max(15, Math.round(size / 5.2))}px` }}
        >
          {label}
        </span>
        {subLabel && <span className={styles.ringSub}>{subLabel}</span>}
      </div>
    </div>
  );
}
