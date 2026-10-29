import type { Role } from "@/graphql/gen/graphql";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import { usersMessages as M } from "./messages";
import { ROLES } from "./status";

/** Role choice as a radio group with what each role may do. */
export function RoleField({
  name,
  value,
  onChange,
  disabled,
  legend = M.role,
  describedBy,
}: {
  name: string;
  value: Role;
  onChange: (role: Role) => void;
  disabled?: boolean | undefined;
  legend?: string;
  describedBy?: string | undefined;
}) {
  return (
    <fieldset className="flex flex-col gap-2" disabled={disabled} aria-describedby={describedBy}>
      <legend className="mb-2 text-sm font-medium">{legend}</legend>
      <div className="grid gap-2 sm:grid-cols-3">
        {ROLES.map((role) => {
          const checked = value === role;
          return (
            <label
              key={role}
              className={cn(
                "flex cursor-pointer flex-col gap-1 rounded-md border bg-surface px-3 py-2.5 transition-colors has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50",
                checked ? "border-primary bg-accent" : "hover:border-border-strong",
                disabled && "cursor-not-allowed opacity-60",
              )}
            >
              <span className="flex items-center gap-2 text-sm font-medium">
                <input
                  type="radio"
                  name={name}
                  value={role}
                  checked={checked}
                  onChange={() => {
                    onChange(role);
                  }}
                  className="size-3.5 accent-primary"
                />
                {messages.roles[role]}
              </span>
              <span className="text-xs text-muted-foreground">{M.roleHints[role]}</span>
            </label>
          );
        })}
      </div>
    </fieldset>
  );
}
