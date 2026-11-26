export type SetupStepStatus = "done" | "todo" | "unknown";

export interface SetupStepState {
  id: string;
  status: SetupStepStatus;
  optional?: boolean;
}

export interface SetupProgressSummary {
  done: number;
  total: number;
  complete: boolean;
}

export function setupProgress(steps: SetupStepState[]): SetupProgressSummary {
  let done = 0;
  let total = 0;

  for (const step of steps) {
    if (step.optional) continue;
    total += 1;
    if (step.status === "done") {
      done += 1;
    }
  }

  return {
    done,
    total,
    complete: total > 0 && done === total,
  };
}
