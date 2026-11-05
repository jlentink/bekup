import { useCallback, useEffect, useRef, useState } from "react";

import { SetupApiError, status, type Status } from "./api";

export type Session = "unknown" | "signed-out" | "signed-in" | "lost";

/**
 * reportPace is the least time between two polls that brought no new step
 * (a report change or the server's wait ending): the live report never
 * turns into a busy loop.
 */
export const reportPace = 1000;

/** pause waits ms or until signal aborts. */
function pause(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const t = setTimeout(resolve, ms);
    signal.addEventListener(
      "abort",
      () => {
        clearTimeout(t);
        resolve();
      },
      { once: true },
    );
  });
}

/**
 * useSetup follows the server's status with long polling: every call waits
 * until the current step or the report changes (or the server's wait
 * ends); polls without a new step are paced (reportPace). onRefused is
 * called once per screen the server publishes again with an error (the
 * previous answer was refused).
 */
export function useSetup(onRefused?: () => void) {
  const [session, setSession] = useState<Session>("unknown");
  const [current, setCurrent] = useState<Status | null>(null);
  const lastStep = useRef<number | undefined>(undefined);
  const [tick, setTick] = useState(0);
  const refusedRef = useRef(onRefused);
  useEffect(() => {
    refusedRef.current = onRefused;
  }, [onRefused]);

  const refresh = useCallback(() => {
    setTick((t) => t + 1);
  }, []);

  useEffect(() => {
    const ctrl = new AbortController();
    let stopped = false;
    const loop = async () => {
      let after: number | undefined = undefined;
      let report: number | undefined = undefined;
      for (;;) {
        try {
          const st = await status(after, report, ctrl.signal);
          if (stopped) return;
          setSession("signed-in");
          setCurrent(st);
          if (st.step?.error && st.step.id !== lastStep.current) refusedRef.current?.();
          lastStep.current = st.step?.id;
          if (st.phase !== "questions") return;
          // Wait for a different step or report (the server answers on
          // every change); 0 while no step waits (setup works), always
          // sent, so the server waits then too.
          const next = st.step?.id ?? 0;
          const sameStep = after === next;
          after = next;
          report = st.reportRev;
          if (sameStep) {
            await pause(reportPace, ctrl.signal);
            if (ctrl.signal.aborted) return;
          }
        } catch (err) {
          if (stopped || ctrl.signal.aborted) return;
          if (err instanceof SetupApiError && err.status === 401) {
            setSession("signed-out");
          } else {
            setSession("lost");
          }
          return;
        }
      }
    };
    void loop();
    return () => {
      stopped = true;
      ctrl.abort();
    };
  }, [tick]);

  return { session, status: current, refresh, setSession };
}
