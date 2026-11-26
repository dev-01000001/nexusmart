import React from "react";

export interface VietQrSvgProps {
  size?: number;
  className?: string;
  decorative?: boolean;
}

export function VietQrSvg({ size = 148, className, decorative = false }: VietQrSvgProps) {
  const finder = (r: number, c: number) =>
    (r < 8 && c < 8) || (r < 8 && c > 12) || (r > 12 && c < 8);
  const cells: Array<[number, number]> = [];
  for (let r = 0; r < 21; r++) {
    for (let c = 0; c < 21; c++) {
      if (!finder(r, c) && (r * 17 + c * 31 + r * c * 7) % 11 < 5) {
        cells.push([r, c]);
      }
    }
  }

  return (
    <svg
      viewBox="0 0 210 210"
      width={size}
      height={size}
      className={className}
      role={decorative ? undefined : "img"}
      aria-label={decorative ? undefined : "Mã VietQR thanh toán đơn hàng"}
      aria-hidden={decorative ? true : undefined}
    >
      <rect width="210" height="210" fill="var(--bg-0)" />
      {[
        [12, 12],
        [138, 12],
        [12, 138],
      ].map(([x, y]) => (
        <g key={`${x}-${y}`}>
          <rect x={x} y={y} width="60" height="60" fill="var(--ink-1)" />
          <rect x={x + 9} y={y + 9} width="42" height="42" fill="var(--bg-0)" />
          <rect x={x + 18} y={y + 18} width="24" height="24" fill="var(--ink-1)" />
        </g>
      ))}
      {cells.map(([r, c]) => (
        <rect
          key={`${r}-${c}`}
          x={12 + c * 9}
          y={12 + r * 9}
          width="7.6"
          height="7.6"
          fill="var(--ink-1)"
        />
      ))}
    </svg>
  );
}
