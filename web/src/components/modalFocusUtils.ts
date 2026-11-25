// Pure focus-trap decision helpers (zero DOM/React dependencies, unit-tested in Node).

export interface ComboboxElementLike {
  getAttribute(name: string): string | null;
  closest?: (selector: string) => unknown;
}

export interface EscapeEventLike {
  isComposing?: boolean;
  keyCode?: number;
}

/** True when focus is inside an expanded ARIA combobox whose own Escape handler should close suggestions first. */
export function isOpenComboboxElement(el: ComboboxElementLike | null | undefined): boolean {
  if (!el) return false;
  if (el.getAttribute("role") === "combobox" && el.getAttribute("aria-expanded") === "true") {
    return true;
  }
  if (typeof el.closest === "function") {
    return Boolean(el.closest('[role="combobox"][aria-expanded="true"]'));
  }
  return false;
}

/**
 * Returns true when Escape must NOT dismiss the modal:
 * - active IME composition (e.g. Vietnamese Telex/VNI)
 * - expanded combobox inside the modal (e.g. search suggestions dropdown)
 */
export function shouldIgnoreModalEscape(
  ev: EscapeEventLike,
  activeElement: ComboboxElementLike | null | undefined
): boolean {
  if (ev.isComposing || ev.keyCode === 229) return true;
  return isOpenComboboxElement(activeElement);
}

export interface TabTrapParams<T> {
  shiftKey: boolean;
  container: T;
  active: T | null;
  containerContainsActive: boolean;
  focusables: readonly T[];
}

/**
 * Computes the element to force-focus on Tab / Shift+Tab to keep focus trapped in `container`.
 * Returns `null` when native browser Tab traversal should proceed normally.
 */
export function resolveTabTrapTarget<T>({
  shiftKey,
  container,
  active,
  containerContainsActive,
  focusables,
}: TabTrapParams<T>): T | null {
  if (focusables.length === 0) {
    return container;
  }
  const first = focusables[0];
  const last = focusables[focusables.length - 1];

  if (shiftKey) {
    if (!containerContainsActive || active === container || active === first) {
      return last;
    }
    return null;
  }

  if (!containerContainsActive || active === container || active === last) {
    return first;
  }
  return null;
}
