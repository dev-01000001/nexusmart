const SHAKE_KEYFRAMES: Keyframe[] = [
  { transform: "translateX(0)" },
  { transform: "translateX(-4px)", offset: 0.22 },
  { transform: "translateX(4px)", offset: 0.48 },
  { transform: "translateX(-2px)", offset: 0.74 },
  { transform: "translateX(0)" },
];

function prefersReducedMotion(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}

/**
 * Triggers a <= 4px horizontal error shake via the Web Animations API without remounting nodes.
 * Automatically skips animation under `prefers-reduced-motion: reduce`.
 */
function shakeElement(el: HTMLElement | null | undefined): void {
  if (!el || prefersReducedMotion() || typeof el.animate !== "function") return;
  el.animate(SHAKE_KEYFRAMES, { duration: 280, easing: "ease-out" });
}

/**
 * Moves focus to the first invalid control (`[aria-invalid="true"]`) inside `form` and
 * shakes each invalid field wrapper (or the form-level alert if no field is invalid).
 */
export function focusAndShakeInvalid(form: HTMLElement | null): void {
  if (!form) return;
  const invalidControls = Array.from(form.querySelectorAll<HTMLElement>('[aria-invalid="true"]'));
  if (invalidControls.length > 0) {
    invalidControls[0].focus();
    const shaken = new Set<HTMLElement>();
    for (const ctrl of invalidControls) {
      const target = ctrl.closest<HTMLElement>("[data-shake-target]") ?? ctrl.parentElement ?? ctrl;
      if (!shaken.has(target)) {
        shaken.add(target);
        shakeElement(target);
      }
    }
    return;
  }
  const alertEl = form.querySelector<HTMLElement>('[data-shake-alert], [role="alert"]');
  shakeElement(alertEl);
}
