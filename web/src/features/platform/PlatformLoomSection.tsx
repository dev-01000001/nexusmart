import React from "react";
import { WORKSHOP_MEDIA } from "@/data/media";
import { AmbientLoomVideo } from "./AmbientLoomVideo";
import styles from "./PlatformLoomSection.module.css";

export function PlatformLoomSection() {
  return (
    <section className={styles.band} aria-labelledby="platform-loom-heading">
      <div className={styles.head}>
        <h2 id="platform-loom-heading" className={`${styles.heading} type-wide`} data-reveal="">
          Từ khung cửi tới cửa hàng
        </h2>
      </div>

      <div className={styles.frameWrap}>
        <div className={styles.loomFrame}>
          <AmbientLoomVideo
            src={WORKSHOP_MEDIA.loomVideo.src}
            poster={WORKSHOP_MEDIA.loomVideo.poster}
            alt={WORKSHOP_MEDIA.loomVideo.alt}
            width={WORKSHOP_MEDIA.loomVideo.width}
            height={WORKSHOP_MEDIA.loomVideo.height}
            className={styles.video}
          />
        </div>
      </div>
    </section>
  );
}
