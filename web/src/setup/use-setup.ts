import { useCallback, useEffect, useRef, useState } from "react";

import { SetupApiError, status, type Status } from "./api";

export type Session = "unknown" | "signed-out" | "signed-in" | "lost";

/**
 * useSetup follows the server's status with long polling: every call waits
 * until the current step changes (or the server's wait ends). onRefused is
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
      for (;;) {
        try {
          const st = await status(after, ctrl.signal);
          if (stopped) return;
          setSession("signed-in");
          setCurrent(st);
          if (st.step?.error && st.step.id !== lastStep.current) refusedRef.current?.();
          lastStep.current = st.step?.id;
          if (st.phase !== "questions") return;
          // Wait for a different step (the server answers on every change).
          after = st.step?.id ?? 0;
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
