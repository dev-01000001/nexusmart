"use client";

import { useEffect } from "react";

/**
 * Progressive-enhancement scroll reveal observer.
 * - Keeps all elements visible when JS is off or `prefers-reduced-motion: reduce` is set.
 * - Marks elements below the viewport as reveal-ready after hydration and animates them once
 *   when they enter the viewport (transform + opacity only).
 */
export function useRevealObserver(containerRef: React.RefObject<HTMLElement | null>, depsKey = "") {
  useEffect(() => {
    const root = containerRef.current;
    if (!root) return;

    const reduced =
      typeof window !== "undefined" &&
      window.matchMedia &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    const nodes = Array.from(root.querySelectorAll<HTMLElement>("[data-cat-reveal]"));
    if (nodes.length === 0) return;

    if (reduced || typeof IntersectionObserver === "undefined") {
      nodes.forEach((el) => {
        el.setAttribute("data-in", "1");
      });
      return;
    }

    const vh = window.innerHeight || 900;
    const toObserve: HTMLElement[] = [];

    nodes.forEach((el) => {
      if (el.getAttribute("data-in") === "1") return;
      const rect = el.getBoundingClientRect();
      if (rect.top < vh * 0.96) {
        // Above or near the initial fold: reveal immediately on next frame so load animates cleanly
        el.setAttribute("data-pending", "1");
        requestAnimationFrame(() => {
          el.setAttribute("data-in", "1");
        });
      } else {
        el.setAttribute("data-pending", "1");
        toObserve.push(el);
      }
    });

    if (toObserve.length === 0) return;

    const io = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            entry.target.setAttribute("data-in", "1");
            io.unobserve(entry.target);
          }
        }
      },
      { rootMargin: "0px 0px -2% 0px", threshold: 0.01 }
    );

    toObserve.forEach((el) => io.observe(el));

    // Safety net: ensure elements scrolled past never stay pending
    const onScroll = () => {
      const limit = window.innerHeight * 1.15;
      for (const el of toObserve) {
        if (el.getAttribute("data-in") === "1") continue;
        const rect = el.getBoundingClientRect();
        if (rect.top < limit) {
          el.setAttribute("data-in", "1");
          io.unobserve(el);
        }
      }
    };

    window.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      io.disconnect();
      window.removeEventListener("scroll", onScroll);
    };
  }, [containerRef, depsKey]);
}
