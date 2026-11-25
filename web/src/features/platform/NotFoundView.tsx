import React from "react";
import Link from "next/link";
import { Button } from "@/components/Button";
import { NexusMark } from "@/components/Icons";
import styles from "./NotFoundView.module.css";

export function NotFoundView() {
  return (
    <div className={styles.page}>
      <header className={styles.topBar}>
        <Link href="/" className={styles.brand} aria-label="NexusMart trang chủ">
          <NexusMark size={20} />
          <span className={`${styles.brandWord} type-wide`}>NexusMart</span>
        </Link>
      </header>

      <main className={styles.center}>
        <p className={`${styles.code} type-poster lines`} aria-hidden="true">
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              404
            </span>
          </span>
        </p>

        <h1 className={styles.message} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          Trang này không tồn tại.
        </h1>

        <div className={styles.action} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
          <Button variant="primary" size="lg" shape="pill" href="/">
            Về trang chủ
          </Button>
        </div>
      </main>
    </div>
  );
}
