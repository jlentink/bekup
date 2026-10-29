import { useApolloClient } from "@apollo/client/react";
import { useCallback, useEffect, useState } from "react";

import { userMessage } from "@/lib/api-error";

import { appendLines, PAGE, type LogBatch, type LogLine } from "./log-model";
import { RunLogsQuery } from "./operations";

/**
 * The log of a run: the stored lines (runLogs, paged), then, while the run
 * is active, the live tail. The page owns the one stream (runActivity,
 * D-197): it opens it while `streamFrom` is set (from `streamFrom.after`), hands
 * every log batch to `applyBatch` and calls `streamEnded` when the stream
 * completed (after the `done` batch and the final run state).
 */
export function useRunLogs(runId: string, active: boolean) {
  const client = useApolloClient();
  const [lines, setLines] = useState<LogLine[]>([]);
  const [endId, setEndId] = useState<string | null>(null);
  const [hasMore, setHasMore] = useState(false);
  const [dropped, setDropped] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  /** The afterId the live stream starts from (fixed per stream). */
  const [streamFrom, setStreamFrom] = useState<{ after: string | null } | null>(null);
  const [done, setDone] = useState(false);

  const loadPage = useCallback(
    async (after: string | null) => {
      try {
        const r = await client.query({
          query: RunLogsQuery,
          variables: { runId, first: PAGE, ...(after ? { afterId: after } : {}) },
          fetchPolicy: "no-cache",
        });
        const page = r.data?.runLogs;
        if (!page) return null;
        setLines((prev) => appendLines(prev, page.lines));
        setEndId(page.endId ?? after);
        setHasMore(page.hasMore);
        setDropped(page.dropped);
        return page;
      } catch (err) {
        setError(userMessage(err));
        return null;
      } finally {
        setLoading(false);
      }
    },
    [client, runId],
  );

  useEffect(() => {
    let cancelled = false;
    client
      .query({ query: RunLogsQuery, variables: { runId, first: PAGE }, fetchPolicy: "no-cache" })
      .then(
        (r) => {
          if (cancelled) return;
          const page = r.data?.runLogs;
          if (page) {
            setLines((prev) => appendLines(prev, page.lines));
            setEndId(page.endId ?? null);
            setHasMore(page.hasMore);
            setDropped(page.dropped);
          }
          setStreamFrom({ after: page?.endId ?? null });
        },
        (err: unknown) => {
          if (!cancelled) setError(userMessage(err));
        },
      )
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [client, runId]);

  // Follow the tail when the run was active on load, until the stream
  // completed (not on `done`: the final run state comes after it).
  const [following, setFollowing] = useState(active);
  const applyBatch = useCallback((batch: LogBatch) => {
    setLines((prev) => appendLines(prev, batch.lines));
    if (batch.endId) setEndId(batch.endId);
    if (batch.done) setDone(true);
  }, []);
  const streamEnded = useCallback(() => {
    setFollowing(false);
  }, []);
  const live = following && streamFrom !== null && !done;

  return {
    lines,
    loading,
    error,
    dropped,
    live,
    /** Open the stream (null: not now). */
    streamFrom: following ? streamFrom : null,
    applyBatch,
    streamEnded,
    /** Older pages remain (a finished run's log is read in pages). */
    hasMore: hasMore && !live,
    loadMore: () => {
      setLoading(true);
      void loadPage(endId);
    },
  };
}
