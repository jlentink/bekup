import type { Role } from "@/graphql/gen/graphql";

import { ownershipBlock, type OwnershipBlock } from "./status";

/** An account as the picker reports it (matches JobOwner's fields). */
export interface PickedUser {
  id: string;
  email: string;
  displayName: string;
  role: Role;
}

export interface PickerCandidate extends PickedUser {
  disabled: boolean;
  deletedAt?: string | null;
}

export interface PickerOption {
  user: PickerCandidate;
  selected: boolean;
  /** Why it cannot be chosen (D-067); null when it can. */
  block: OwnershipBlock | null;
}

/**
 * The options for an ownership grant: every candidate, with accounts that
 * cannot own a job (role other than User, disabled, deleted) kept visible
 * but blocked, so the admin sees why. Deleted accounts are dropped.
 */
export function pickerOptions(
  candidates: readonly PickerCandidate[],
  selectedIds: ReadonlySet<string>,
  excludeIds: ReadonlySet<string> = new Set(),
): PickerOption[] {
  return candidates
    .filter((u) => !u.deletedAt && !excludeIds.has(u.id))
    .map((u) => ({ user: u, selected: selectedIds.has(u.id), block: ownershipBlock(u) }))
    .sort((a, b) => Number(a.block !== null) - Number(b.block !== null));
}
