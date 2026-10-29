import { skipToken, useQuery } from "@apollo/client/react";
import { Check, Info, Plus, Search, X } from "lucide-react";
import { useEffect, useId, useState } from "react";

import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiCode, hasApiCode, userMessage } from "@/lib/api-error";
import { cn } from "@/lib/utils";

import { usersMessages as M } from "./messages";
import { UserPickerQuery } from "./operations";
import { pickerOptions, type PickedUser, type PickerCandidate } from "./user-picker-model";
import { RoleTag } from "./user-badges";

export interface UserPickerProps {
  /** The chosen accounts. */
  value: readonly PickedUser[];
  onChange: (next: PickedUser[]) => void;
  /** Accounts not offered (e.g. the current maintainer, who cannot add themselves). */
  excludeIds?: readonly string[];
  label?: string;
  disabled?: boolean;
  className?: string;
}

/**
 * Reusable picker for backup owners (job owners panel, user pages). Only
 * accounts with role User are selectable (D-067); the server re-checks
 * every grant. Backed by the staff `userDirectory` (D-184), so admins and
 * maintainers can use it; for other roles it explains that staff assign
 * owners.
 */
export function UserPicker({
  value,
  onChange,
  excludeIds = [],
  label = M.pickerLabel,
  disabled = false,
  className,
}: UserPickerProps) {
  const searchId = useId();
  const hintId = useId();
  const [search, setSearch] = useState("");
  const [term, setTerm] = useState("");
  useEffect(() => {
    const t = setTimeout(() => {
      setTerm(search.trim());
    }, 250);
    return () => {
      clearTimeout(t);
    };
  }, [search]);

  const { data, previousData, error } = useQuery(
    UserPickerQuery,
    disabled
      ? skipToken
      : { variables: { search: term || null }, fetchPolicy: "cache-and-network" },
  );
  const shown = data ?? previousData;
  const selectedIds = new Set(value.map((u) => u.id));
  const options = pickerOptions(
    (shown?.userDirectory.edges ?? []).map((e) => e.node),
    selectedIds,
    new Set(excludeIds),
  );

  const toggle = (u: PickerCandidate, selected: boolean) => {
    if (selected) {
      onChange(value.filter((v) => v.id !== u.id));
    } else {
      onChange([...value, { id: u.id, email: u.email, displayName: u.displayName, role: u.role }]);
    }
  };

  return (
    <div className={cn("flex flex-col gap-3", className)}>
      <div className="flex flex-col gap-1">
        <label htmlFor={searchId} className="text-sm font-medium">
          {label}
        </label>
        <p id={hintId} className="text-xs text-muted-foreground">
          {M.pickerRoleOnly}
        </p>
      </div>

      {value.length > 0 ? (
        <ul aria-label={M.pickerSelected} className="flex flex-wrap gap-1.5">
          {value.map((u) => (
            <li
              key={u.id}
              className="inline-flex h-7 items-center gap-1.5 rounded-sm border border-primary/30 bg-accent pr-1 pl-2 text-xs"
            >
              <span className="font-medium">{u.displayName}</span>
              <span className="font-mono text-2xs text-muted-foreground">{u.email}</span>
              <button
                type="button"
                disabled={disabled}
                aria-label={M.pickerRemove(u.displayName)}
                className="inline-flex size-5 items-center justify-center rounded-sm text-muted-foreground hover:bg-background hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
                onClick={() => {
                  onChange(value.filter((v) => v.id !== u.id));
                }}
              >
                <X aria-hidden="true" className="size-3" />
              </button>
            </li>
          ))}
        </ul>
      ) : null}

      {error && !shown ? (
        <p
          role="note"
          className="flex gap-2 rounded-md border border-rule bg-surface-sunken px-3 py-2.5 text-sm text-muted-foreground"
        >
          <Info aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
          {hasApiCode(error, ApiCode.forbidden) ? M.pickerForbidden : userMessage(error)}
        </p>
      ) : (
        <div className="flex flex-col overflow-hidden rounded-md border bg-surface">
          <div className="relative border-b border-rule">
            <Search
              aria-hidden="true"
              className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              id={searchId}
              type="search"
              disabled={disabled}
              aria-describedby={hintId}
              placeholder={M.pickerSearch}
              className="rounded-none border-0 pl-8 shadow-none focus-visible:ring-0"
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
              }}
            />
          </div>
          {!shown && !disabled ? (
            <div className="flex flex-col gap-1 p-2" aria-busy="true">
              <Skeleton className="h-9 w-full" />
              <Skeleton className="h-9 w-full" />
            </div>
          ) : options.length === 0 ? (
            <p className="px-3 py-4 text-center text-sm text-muted-foreground">{M.pickerEmpty}</p>
          ) : (
            <ul aria-label={label} className="max-h-64 divide-y divide-rule overflow-y-auto">
              {options.map(({ user: u, selected, block }) => {
                const reasonId = `${searchId}-${u.id}-reason`;
                return (
                  <li key={u.id}>
                    <button
                      type="button"
                      aria-pressed={selected}
                      aria-disabled={block !== null || disabled ? true : undefined}
                      aria-describedby={block ? reasonId : undefined}
                      data-blocked={block ?? undefined}
                      className={cn(
                        "flex w-full items-center gap-3 px-3 py-2 text-left transition-colors focus-visible:bg-accent focus-visible:outline-none",
                        block === null && !disabled
                          ? "hover:bg-accent/50"
                          : "cursor-not-allowed opacity-60",
                        selected && "bg-accent/60",
                      )}
                      onClick={() => {
                        if (block !== null || disabled) return;
                        toggle(u, selected);
                      }}
                    >
                      <span
                        aria-hidden="true"
                        className={cn(
                          "inline-flex size-4 shrink-0 items-center justify-center rounded-sm border",
                          selected
                            ? "border-primary bg-primary text-primary-foreground"
                            : "border-border-strong",
                        )}
                      >
                        {selected ? (
                          <Check className="size-3" />
                        ) : block === null ? (
                          <Plus className="size-3 text-muted-foreground" />
                        ) : null}
                      </span>
                      <span className="flex min-w-0 flex-1 flex-col">
                        <span className="truncate text-sm font-medium">{u.displayName}</span>
                        <span className="truncate font-mono text-2xs text-muted-foreground">
                          {u.email}
                        </span>
                        {block ? (
                          <span id={reasonId} className="text-2xs text-muted-foreground">
                            {M.pickerReason[block]}
                          </span>
                        ) : null}
                      </span>
                      <RoleTag role={u.role} />
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
