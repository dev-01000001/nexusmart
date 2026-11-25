// Run: node --test web/src/components/modalFocus.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  isOpenComboboxElement,
  resolveTabTrapTarget,
  shouldIgnoreModalEscape,
} from "./modalFocusUtils.ts";

function fakeEl(attrs = {}, ancestorMatches = false) {
  return {
    getAttribute(name) {
      return name in attrs ? attrs[name] : null;
    },
    closest() {
      return ancestorMatches ? {} : null;
    },
  };
}

test("shouldIgnoreModalEscape yields to IME composition and expanded comboboxes", () => {
  const normalInput = fakeEl({});
  const collapsedCombo = fakeEl({ role: "combobox", "aria-expanded": "false" });
  const expandedCombo = fakeEl({ role: "combobox", "aria-expanded": "true" });
  const insideExpandedCombo = fakeEl({}, true);

  assert.equal(shouldIgnoreModalEscape({ isComposing: true }, normalInput), true);
  assert.equal(shouldIgnoreModalEscape({ keyCode: 229 }, normalInput), true);
  assert.equal(shouldIgnoreModalEscape({ isComposing: false }, expandedCombo), true);
  assert.equal(shouldIgnoreModalEscape({ isComposing: false }, insideExpandedCombo), true);
  assert.equal(isOpenComboboxElement(expandedCombo), true);
  assert.equal(isOpenComboboxElement(collapsedCombo), false);
  assert.equal(shouldIgnoreModalEscape({ isComposing: false }, collapsedCombo), false);
  assert.equal(shouldIgnoreModalEscape({ isComposing: false }, normalInput), false);
  assert.equal(shouldIgnoreModalEscape({}, null), false);
});

test("resolveTabTrapTarget traps Shift+Tab from container/first and Tab from container/last", () => {
  const container = { id: "dialog" };
  const first = { id: "first" };
  const mid = { id: "mid" };
  const last = { id: "last" };
  const outside = { id: "outside" };
  const focusables = [first, mid, last];

  // Shift+Tab when focus is on container itself must wrap to last (M13 regression).
  assert.equal(
    resolveTabTrapTarget({
      shiftKey: true,
      container,
      active: container,
      containerContainsActive: true,
      focusables,
    }),
    last
  );
  // Shift+Tab on first wraps to last; on mid lets browser move naturally.
  assert.equal(
    resolveTabTrapTarget({
      shiftKey: true,
      container,
      active: first,
      containerContainsActive: true,
      focusables,
    }),
    last
  );
  assert.equal(
    resolveTabTrapTarget({
      shiftKey: true,
      container,
      active: mid,
      containerContainsActive: true,
      focusables,
    }),
    null
  );
  // Forward Tab from container or last wraps to first; on mid lets browser move naturally.
  assert.equal(
    resolveTabTrapTarget({
      shiftKey: false,
      container,
      active: container,
      containerContainsActive: true,
      focusables,
    }),
    first
  );
  assert.equal(
    resolveTabTrapTarget({
      shiftKey: false,
      container,
      active: last,
      containerContainsActive: true,
      focusables,
    }),
    first
  );
  assert.equal(
    resolveTabTrapTarget({
      shiftKey: false,
      container,
      active: mid,
      containerContainsActive: true,
      focusables,
    }),
    null
  );
  // Outside active focus is pulled back in; empty focusables focus container.
  assert.equal(
    resolveTabTrapTarget({
      shiftKey: false,
      container,
      active: outside,
      containerContainsActive: false,
      focusables,
    }),
    first
  );
  assert.equal(
    resolveTabTrapTarget({
      shiftKey: true,
      container,
      active: container,
      containerContainsActive: true,
      focusables: [],
    }),
    container
  );
});
