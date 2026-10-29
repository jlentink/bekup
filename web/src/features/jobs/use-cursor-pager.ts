import { useCallback, useState } from "react";

/**
 * Cursor pagination state for forward-only connections: the cursors of
 * the pages visited so far, so "previous" works without backward cursors.
 * A new `key` (e.g. the serialized filter) starts again at page 1.
 */
export function useCursorPager(key: string) {
  const [state, setState] = useState<{ key: string; stack: (string | null)[] }>({
    key,
    stack: [null],
  });
  const stack = state.key === key ? state.stack : [null];
  const after = stack[stack.length - 1] ?? null;
  const next = useCallback(
    (endCursor: string | null | undefined) => {
      if (!endCursor) return;
      setState((s) => ({ key, stack: [...(s.key === key ? s.stack : [null]), endCursor] }));
    },
    [key],
  );
  const prev = useCallback(() => {
    setState((s) => {
      const cur = s.key === key ? s.stack : [null];
      return { key, stack: cur.length > 1 ? cur.slice(0, -1) : cur };
    });
  }, [key]);
  return { after, page: stack.length, next, prev };
}
