import React from "react";
import Link from "next/link";

export default function NotFoundPage() {
  return (
    <main
      style={{
        minHeight: "68vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: "48px var(--margin, 24px)",
        background: "var(--bg)",
        color: "var(--ink)",
      }}
    >
      <div
        style={{
          maxWidth: 480,
          width: "100%",
          padding: "32px 28px",
          background: "var(--surface-white)",
          border: "1px solid var(--border)",
          borderRadius: 6,
          display: "grid",
          gap: 14,
        }}
      >
        <p
          style={{
            margin: 0,
            fontSize: 13,
            fontWeight: 600,
            letterSpacing: "0.06em",
            textTransform: "uppercase",
            color: "var(--accent)",
            fontVariantNumeric: "tabular-nums",
          }}
        >
          Lỗi 404
        </p>
        <h1
          style={{
            margin: 0,
            fontSize: 26,
            lineHeight: 1.25,
            fontWeight: 600,
            color: "var(--ink)",
          }}
        >
          Không tìm thấy trang bạn yêu cầu
        </h1>
        <p
          style={{
            margin: 0,
            fontSize: 15,
            lineHeight: 1.55,
            color: "var(--muted)",
          }}
        >
          Đường dẫn có thể đã thay đổi, sản phẩm đã ngừng kinh doanh hoặc không tồn tại trên
          NexusMart.
        </p>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 10, paddingTop: 6 }}>
          <Link
            href="/"
            style={{
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              padding: "10px 18px",
              borderRadius: 4,
              background: "var(--accent)",
              color: "#ffffff",
              fontSize: 14,
              fontWeight: 500,
              textDecoration: "none",
            }}
          >
            Về trang chủ
          </Link>
          <Link
            href="/danh-muc"
            style={{
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              padding: "10px 18px",
              borderRadius: 4,
              background: "transparent",
              border: "1px solid var(--border-strong)",
              color: "var(--ink)",
              fontSize: 14,
              fontWeight: 500,
              textDecoration: "none",
            }}
          >
            Xem danh mục sản phẩm
          </Link>
        </div>
      </div>
    </main>
  );
}
