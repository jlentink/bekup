import { useQuery } from "@apollo/client/react";
import { ChevronLeft, ChevronRight, Search, ShieldCheck, UserPlus } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { Link } from "react-router";

import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useSessionUser } from "@/features/auth/use-session-user";
import { getFragmentData } from "@/graphql/gen";
import type { Role, UserFilter } from "@/graphql/gen/graphql";
import { userMessage } from "@/lib/api-error";
import { formatDateTime, formatRelative } from "@/lib/format";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import { CreateUserDialog } from "./create-user-dialog";
import { usersMessages as M } from "./messages";
import { AdminUserFields, UsersQuery } from "./operations";
import { ROLES } from "./status";
import { TemporaryPasswordDialog, type IssuedPassword } from "./temporary-password-dialog";
import { AccountFlags, LastSeen, Monogram, RoleTag, TwoFactorCell } from "./user-badges";

const PAGE_SIZE = 50;

type StatusFilter = "any" | "active" | "disabled";

/** Native select styled like Input (accessible and test-friendly). */
export const selectClass =
  "h-9 rounded-md border border-input bg-surface px-2.5 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 dark:bg-surface-sunken";

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => {
      setV(value);
    }, ms);
    return () => {
      clearTimeout(t);
    };
  }, [value, ms]);
  return v;
}

/** The account ledger (admin): search, filters, cursor pages, create. */
export function UsersPage() {
  const me = useSessionUser();
  const searchId = useId();
  const roleId = useId();
  const statusId = useId();
  const [search, setSearch] = useState("");
  const [role, setRole] = useState<Role | "any">("any");
  const [status, setStatus] = useState<StatusFilter>("any");
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const [creating, setCreating] = useState(false);
  const [issued, setIssued] = useState<IssuedPassword | null>(null);

  const term = useDebounced(search.trim(), 250);
  const filter: UserFilter = {
    ...(term ? { search: term } : {}),
    ...(role !== "any" ? { role } : {}),
    ...(status !== "any" ? { disabled: status === "disabled" } : {}),
    ...(includeDeleted ? { includeDeleted: true } : {}),
  };
  // Cursor stack: cursors[i] is the `after` of page i (page 0: none). A
  // filter change starts again at page 0.
  const filterKey = JSON.stringify(filter);
  const [pager, setPager] = useState<{ key: string; cursors: (string | null)[] }>({
    key: filterKey,
    cursors: [null],
  });
  const cursors = pager.key === filterKey ? pager.cursors : [null];
  const setCursors = (next: (string | null)[]) => {
    setPager({ key: filterKey, cursors: next });
  };

  const page = cursors.length - 1;
  const { data, previousData, error, loading, refetch } = useQuery(UsersQuery, {
    variables: { filter, first: PAGE_SIZE, after: cursors[page] ?? null },
    fetchPolicy: "cache-and-network",
  });
  const shown = data ?? previousData;
  const rows = (shown?.users.edges ?? []).map((e) => getFragmentData(AdminUserFields, e.node));
  const pageInfo = shown?.users.pageInfo;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow={M.eyebrow}
        title={M.title}
        description={M.lead}
        actions={
          <>
            <Button asChild variant="outline" size="sm">
              <Link to="/admin/two-factor">
                <ShieldCheck aria-hidden="true" /> {M.twoFactorPolicy}
              </Link>
            </Button>
            <Button
              size="sm"
              onClick={() => {
                setCreating(true);
              }}
            >
              <UserPlus aria-hidden="true" /> {M.create}
            </Button>
          </>
        }
      />

      <div role="search" className="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-end">
        <div className="flex min-w-64 flex-1 flex-col gap-1.5">
          <label htmlFor={searchId} className="eyebrow">
            {M.searchLabel}
          </label>
          <div className="relative">
            <Search
              aria-hidden="true"
              className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              id={searchId}
              type="search"
              value={search}
              placeholder={M.searchPlaceholder}
              className="pl-8"
              onChange={(e) => {
                setSearch(e.target.value);
              }}
            />
          </div>
        </div>
        <div className="flex flex-col gap-1.5">
          <label htmlFor={roleId} className="eyebrow">
            {M.roleFilter}
          </label>
          <select
            id={roleId}
            className={selectClass}
            value={role}
            onChange={(e) => {
              setRole(e.target.value as Role | "any");
            }}
          >
            <option value="any">{M.anyRole}</option>
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {messages.roles[r]}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1.5">
          <label htmlFor={statusId} className="eyebrow">
            {M.statusFilter}
          </label>
          <select
            id={statusId}
            className={selectClass}
            value={status}
            onChange={(e) => {
              setStatus(e.target.value as StatusFilter);
            }}
          >
            <option value="any">{M.anyStatus}</option>
            <option value="active">{M.active}</option>
            <option value="disabled">{M.disabledFilter}</option>
          </select>
        </div>
        <label className="flex h-9 items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={includeDeleted}
            onChange={(e) => {
              setIncludeDeleted(e.target.checked);
            }}
            className="size-4 accent-primary"
          />
          {M.includeDeleted}
        </label>
      </div>

      {error && !shown ? (
        <Alert variant="destructive">
          <AlertDescription className="flex flex-wrap items-center gap-3">
            {userMessage(error)}
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                void refetch().catch(() => undefined);
              }}
            >
              {messages.common.retry}
            </Button>
          </AlertDescription>
        </Alert>
      ) : !shown ? (
        <div className="flex flex-col gap-2" aria-busy="true">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-14 w-full" />
          ))}
        </div>
      ) : (
        <div
          className={cn(
            "overflow-x-auto rounded-md border bg-surface transition-opacity",
            loading && "opacity-70",
          )}
          aria-busy={loading}
        >
          <table className="w-full min-w-[52rem] border-collapse text-sm">
            <caption className="sr-only">{M.title}</caption>
            <thead>
              <tr className="border-b border-rule text-left">
                {[
                  M.columns.user,
                  M.columns.role,
                  M.columns.status,
                  M.columns.twoFactor,
                  M.columns.lastSeen,
                ].map((h) => (
                  <th key={h} scope="col" className="eyebrow px-4 py-2.5 font-medium">
                    {h}
                  </th>
                ))}
                <th scope="col" className="eyebrow px-4 py-2.5 text-right font-medium">
                  {M.columns.created}
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-rule">
              {rows.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-10 text-center text-muted-foreground">
                    {M.empty}
                  </td>
                </tr>
              ) : (
                rows.map((u) => (
                  <tr
                    key={u.id}
                    className={cn(
                      "group transition-colors hover:bg-accent/40",
                      u.deletedAt && "text-muted-foreground",
                    )}
                  >
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-3">
                        <Monogram name={u.displayName} />
                        <div className="flex min-w-0 flex-col">
                          <Link
                            to={`/admin/users/${u.id}`}
                            className="truncate font-medium underline-offset-4 group-hover:underline focus-visible:underline"
                          >
                            {u.displayName}
                          </Link>
                          <span className="truncate font-mono text-[0.75rem] text-muted-foreground">
                            {u.email}
                          </span>
                        </div>
                        {u.id === me.id ? (
                          <span className="eyebrow rounded-sm border border-rule px-1.5">
                            {M.youTag}
                          </span>
                        ) : null}
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <RoleTag role={u.role} />
                    </td>
                    <td className="px-4 py-3">
                      <AccountFlags user={u} />
                    </td>
                    <td className="px-4 py-3">
                      <TwoFactorCell user={u} />
                    </td>
                    <td className="px-4 py-3 font-mono text-2xs whitespace-nowrap">
                      <LastSeen at={u.lastSeenAt} />
                    </td>
                    <td
                      className="px-4 py-3 text-right font-mono text-2xs whitespace-nowrap text-muted-foreground"
                      title={formatDateTime(u.createdAt)}
                    >
                      {formatRelative(u.createdAt)}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}

      {shown && (page > 0 || pageInfo?.hasNextPage) ? (
        <nav aria-label={M.title} className="flex items-center justify-between gap-3">
          <Button
            variant="outline"
            size="sm"
            disabled={page === 0}
            onClick={() => {
              setCursors(cursors.slice(0, -1));
            }}
          >
            <ChevronLeft aria-hidden="true" /> {M.previous}
          </Button>
          <span className="font-mono text-2xs text-muted-foreground">{M.page(page + 1)}</span>
          <Button
            variant="outline"
            size="sm"
            disabled={!pageInfo?.hasNextPage || !pageInfo.endCursor}
            onClick={() => {
              const next = pageInfo?.endCursor;
              if (next) setCursors([...cursors, next]);
            }}
          >
            {M.next} <ChevronRight aria-hidden="true" />
          </Button>
        </nav>
      ) : null}

      <CreateUserDialog open={creating} onOpenChange={setCreating} onCreated={setIssued} />
      <TemporaryPasswordDialog
        issued={issued}
        onDone={() => {
          setIssued(null);
        }}
      />
    </div>
  );
}
