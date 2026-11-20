"use client";

import React from "react";
import shared from "./adminShared.module.css";

export interface ToggleSwitchProps {
  id: string;
  checked: boolean;
  label: string;
  onChange: (next: boolean) => void;
}

export function ToggleSwitch({ id, checked, label, onChange }: ToggleSwitchProps) {
  return (
    <button
      id={id}
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      className={`${shared.toggleBtn} ${checked ? shared.toggleBtnOn : ""}`}
      onClick={() => onChange(!checked)}
    >
      <span className={shared.toggleKnob} />
    </button>
  );
}
