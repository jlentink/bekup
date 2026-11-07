import { skipToken, useMutation, useQuery } from "@apollo/client/react";
import { Loader2, Search } from "lucide-react";
import { useEffect, useId, useMemo, useState, type SyntheticEvent } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { isStepUpCancelled, useStepUp } from "@/features/auth/use-step-up";
import { firstApiError, userMessage } from "@/lib/api-error";
import { messages } from "@/lib/messages";
import { cn } from "@/lib/utils";

import { AddressEditor, type AddressDraft } from "./address-editor";
import { T } from "./messages";
import {
  addressProblem,
  DEFAULT_EXPIRY,
  EXPIRY_CHOICES,
  expiryInput,
  splitAddresses,
  type ExpiryPreset,
} from "./model";
import { ApiAccessJobsQuery, ApiTokensQuery, CreateApiTokenMutation } from "./operations";
import type { IssuedToken } from "./show-once-dialog";

/** Jobs per search page (the server searches by name). */
const JOB_PAGE = 50;

type FieldErrors = Partial<
  Record<"name" | "jobIds" | "expiresAt" | "allowedCidrs" | "form", string>
>;

/**
 * Creates an API token (D-220): name, jobs (the panel's job preselected;
 * the list holds the jobs the caller may use, searched on the server, the
 * chosen ones always on top), an expiry preset and the client addresses. Submitting asks for a step-up; the plaintext goes
 * straight to onCreated (the show-once dialog) and is never cached. Mount
 * it only while open, so every opening starts empty.
 */
export function CreateDialog({
  open,
  onOpenChange,
  presetJobId,
  presetJobName,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  presetJobId?: string | undefined;
  /** The preselected job's name (shown while it is not in the results). */
  presetJobName?: string | undefined;
  onCreated: (t: IssuedToken) => void;
}) {
  const { withStepUp } = useStepUp();
  const [filter, setFilter] = useState("");
  const [term, setTerm] = useState("");
  useEffect(() => {
    const t = setTimeout(() => {
      setTerm(filter.trim());
    }, 250);
    return () => {
      clearTimeout(t);
    };
  }, [filter]);
  const jobsQ = useQuery(
    ApiAccessJobsQuery,
    open ? { variables: { search: term || null, first: JOB_PAGE } } : skipToken,
  );
  const [create] = useMutation(CreateApiTokenMutation, {
    fetchPolicy: "no-cache",
    refetchQueries: [ApiTokensQuery],
  });
  const [name, setName] = useState("");
  // The chosen jobs with their names, so they stay listed whatever the
  // search shows.
  const [jobs, setJobs] = useState<ReadonlyMap<string, string>>(
    () => new Map(presetJobId ? [[presetJobId, presetJobName ?? presetJobId]] : []),
  );
  const [expiry, setExpiry] = useState<ExpiryPreset>(DEFAULT_EXPIRY);
  const [addresses, setAddresses] = useState<AddressDraft>({ text: "", allowAny: false });
  const [errors, setErrors] = useState<FieldErrors>({});
  const [busy, setBusy] = useState(false);
  const ids = { name: useId(), nameHint: useId(), jobs: useId(), filter: useId(), expiry: useId() };

  const page = jobsQ.data ?? jobsQ.previousData;
  const results = useMemo(() => page?.jobs.edges.map((e) => e.node) ?? [], [page]);
  const more = page?.jobs.pageInfo.hasNextPage ?? false;
  // Chosen jobs first (their names as last seen), then the other results.
  const shown = useMemo(() => {
    const names = new Map(results.map((j) => [j.id, j.name]));
    const chosen = [...jobs].map(([id, name]) => ({ id, name: names.get(id) ?? name }));
    return [...chosen, ...results.filter((j) => !jobs.has(j.id))];
  }, [jobs, results]);
  const searchable = term !== "" || filter !== "" || more || results.length > 8;

  const submit = (e: SyntheticEvent) => {
    e.preventDefault();
    const list = splitAddresses(addresses.text);
    const next: FieldErrors = {};
    const n = name.trim();
    if (n === "" || n.length > 100) next.name = T.create.nameMissing;
    if (jobs.size === 0) next.jobIds = T.create.jobsMissing;
    if (!addresses.allowAny) {
      const p = addressProblem(list);
      if (p) next.allowedCidrs = p;
    }
    setErrors(next);
    if (Object.keys(next).length > 0) return;
    setBusy(true);
    const input = {
      name: n,
      jobIds: [...jobs.keys()],
      ...expiryInput(expiry),
      allowedCidrs: addresses.allowAny ? [] : list,
      allowAnyAddress: addresses.allowAny,
    };
    withStepUp(() => create({ variables: { input } }))
      .then((res) => {
        const created = res.data?.createApiToken;
        if (!created) throw new Error("no token returned");
        const jobId =
          presetJobId && created.token.jobs.some((j) => j.id === presetJobId)
            ? presetJobId
            : (created.token.jobs[0]?.id ?? input.jobIds[0] ?? "");
        onOpenChange(false);
        onCreated({ plaintext: created.plaintext, name: created.token.name, jobId });
      })
      .catch((err: unknown) => {
        if (isStepUpCancelled(err)) return;
        const f = firstApiError(err)?.field;
        const key =
          f === "name" || f === "jobIds" || f === "expiresAt" || f === "allowedCidrs" ? f : "form";
        setErrors({ [key]: userMessage(err) });
      })
      .finally(() => {
        setBusy(false);
      });
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!busy) onOpenChange(o);
      }}
    >
      <DialogContent className="max-h-[90vh] gap-5 overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle className="font-display text-2xl font-normal">{T.create.title}</DialogTitle>
          <DialogDescription>{T.create.lead}</DialogDescription>
        </DialogHeader>
        <form noValidate onSubmit={submit} className="flex flex-col gap-5">
          <div className="grid gap-2">
            <Label htmlFor={ids.name}>{T.create.name}</Label>
            <Input
              id={ids.name}
              value={name}
              maxLength={100}
              autoComplete="off"
              aria-invalid={errors.name ? true : undefined}
              aria-describedby={ids.nameHint}
              onChange={(e) => {
                setName(e.target.value);
              }}
            />
            <p
              id={ids.nameHint}
              className={cn("text-xs", errors.name ? "text-destructive" : "text-muted-foreground")}
            >
              {errors.name ?? T.create.nameHint}
            </p>
          </div>

          <fieldset className="flex flex-col gap-2" aria-describedby={ids.jobs}>
            <legend className="mb-1 text-sm font-medium">{T.create.jobs}</legend>
            {searchable ? (
              <div className="relative">
                <Search
                  aria-hidden="true"
                  className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
                />
                <Input
                  type="search"
                  className="pl-8"
                  aria-label={T.create.jobsFilter}
                  placeholder={T.create.jobsFilter}
                  value={filter}
                  onChange={(e) => {
                    setFilter(e.target.value);
                  }}
                />
              </div>
            ) : null}
            {!page && jobsQ.error ? (
              <p className="text-xs text-destructive">{userMessage(jobsQ.error)}</p>
            ) : !page ? (
              <p className="text-xs text-muted-foreground">{T.create.jobsLoading}</p>
            ) : shown.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                {term === "" ? T.create.noJobs : T.create.jobsNoMatch}
              </p>
            ) : (
              <ul className="flex max-h-48 flex-col divide-y divide-rule overflow-y-auto rounded-md border">
                {shown.map((j) => (
                  <li key={j.id}>
                    <label className="flex cursor-pointer items-center gap-2 px-3 py-2 text-sm hover:bg-accent">
                      <input
                        type="checkbox"
                        className="size-4 accent-primary"
                        checked={jobs.has(j.id)}
                        onChange={(e) => {
                          const next = new Map(jobs);
                          if (e.target.checked) next.set(j.id, j.name);
                          else next.delete(j.id);
                          setJobs(next);
                        }}
                      />
                      {j.name}
                    </label>
                  </li>
                ))}
              </ul>
            )}
            {more ? (
              <p className="text-xs text-muted-foreground">{T.create.jobsTruncated}</p>
            ) : null}
            <p
              id={ids.jobs}
              className={cn(
                "text-xs",
                errors.jobIds ? "text-destructive" : "text-muted-foreground",
              )}
            >
              {errors.jobIds ?? T.create.jobsHint}
            </p>
          </fieldset>

          <fieldset className="flex flex-col gap-2" aria-describedby={ids.expiry}>
            <legend className="mb-1 text-sm font-medium">{T.create.expiry}</legend>
            <div className="flex flex-wrap gap-2">
              {EXPIRY_CHOICES.map((d) => (
                <label
                  key={d}
                  className={cn(
                    "flex cursor-pointer items-center gap-2 rounded-md border bg-surface px-3 py-1.5 text-sm has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50",
                    expiry === d ? "border-primary/50 bg-accent" : "hover:border-border-strong",
                  )}
                >
                  <input
                    type="radio"
                    name="api-token-expiry"
                    className="size-4 accent-primary"
                    checked={expiry === d}
                    onChange={() => {
                      setExpiry(d);
                    }}
                  />
                  {d === "never" ? T.create.never : T.create.days(d)}
                </label>
              ))}
            </div>
            <p
              id={ids.expiry}
              className={cn(
                "text-xs",
                errors.expiresAt ? "text-destructive" : "text-muted-foreground",
              )}
            >
              {errors.expiresAt ?? (expiry === "never" ? T.create.neverHint : T.create.expiryHint)}
            </p>
          </fieldset>

          <AddressEditor value={addresses} onChange={setAddresses} error={errors.allowedCidrs} />

          {errors.form ? (
            <Alert variant="destructive">
              <AlertDescription>{errors.form}</AlertDescription>
            </Alert>
          ) : null}
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={busy}>
                {messages.common.cancel}
              </Button>
            </DialogClose>
            <Button type="submit" disabled={busy}>
              {busy ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              {T.create.submit}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
