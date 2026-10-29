import { ChevronDown, Download, Play, RotateCcw, Trash2 } from "lucide-react";
import type { ReactNode } from "react";
import { toast } from "sonner";

import { LogoMark, Wordmark } from "@/components/brand/logo";
import { PageHeader } from "@/components/page-header";
import { navForRole, type Role } from "@/components/shell/nav";
import { SecurityBadge } from "@/components/status/security-badge";
import { RUN_STATUSES, SECURITY_FLAGS } from "@/components/status/status";
import { StatusBadge, StatusDot } from "@/components/status/status-badge";
import { ThemeToggle } from "@/components/theme-toggle";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";

/* ------------------------------------------------------------------------ */

const SURFACE_TOKENS = [
  "background",
  "surface",
  "surface-sunken",
  "foreground",
  "muted",
  "muted-foreground",
  "border",
  "border-strong",
  "rule",
] as const;
const CHROME_TOKENS = ["primary", "secondary", "accent", "destructive", "input", "ring"] as const;
const STATUS_TOKENS = [
  "success",
  "partial",
  "failed",
  "running",
  "missed",
  "insecure",
  "neutral",
] as const;

const TYPE_SCALE = [
  ["text-display-lg", "font-display text-display-lg", "Display large · 56"],
  ["text-display", "font-display text-display", "Display · 40"],
  ["text-2xl", "text-2xl font-semibold tracking-tight", "Heading · 24"],
  ["text-lg", "text-lg font-semibold", "Subheading · 18"],
  ["text-sm", "text-sm", "Body (UI default) · 14"],
  ["text-xs", "text-xs text-muted-foreground", "Caption · 12"],
  ["eyebrow", "eyebrow", "Eyebrow · mono 11 caps"],
  ["font-mono", "font-mono text-sm", "Mono · sha256 9f2c…e41a"],
] as const;

const SPACING = [1, 2, 3, 4, 6, 8, 12, 16] as const;
const RADII = ["rounded-xs", "rounded-sm", "rounded-md", "rounded-lg", "rounded-xl"] as const;
const ROLES: readonly Role[] = ["ADMIN", "MAINTAINER", "USER"];

const SAMPLE_RUNS = [
  {
    job: "billing-pg-hourly",
    db: "billing",
    status: "success",
    size: "1.82 GiB",
    at: "2026-10-26 09:00",
  },
  {
    job: "crm-mysql-nightly",
    db: "crm, crm_audit",
    status: "partial",
    size: "640.3 MiB",
    at: "2026-10-26 02:00",
  },
  { job: "auth-pg-hourly", db: "auth", status: "running", size: "—", at: "2026-10-26 10:00" },
  { job: "legacy-maria-daily", db: "shop", status: "failed", size: "—", at: "2026-10-26 03:15" },
  {
    job: "analytics-pg-weekly",
    db: "warehouse",
    status: "missed",
    size: "—",
    at: "2026-10-25 23:00",
  },
] as const;

/* ------------------------------------------------------------------------ */

function Section({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section
      aria-labelledby={id}
      className="grid gap-6 border-t border-rule pt-8 lg:grid-cols-[12rem_1fr]"
    >
      <h2 id={id} className="eyebrow pt-1 text-foreground">
        {title}
      </h2>
      <div className="flex min-w-0 flex-col gap-6">{children}</div>
    </section>
  );
}

function Swatch({ token }: { token: string }) {
  return (
    <figure className="flex flex-col gap-1.5">
      {/* style={} is applied through the CSSOM, which CSP does not restrict. */}
      <div className="h-12 rounded-md border" style={{ background: `var(--${token})` }} />
      <figcaption className="font-mono text-2xs text-muted-foreground">--{token}</figcaption>
    </figure>
  );
}

function StatusSwatch({ status }: { status: string }) {
  return (
    <figure className="flex flex-col overflow-hidden rounded-md border">
      <div className="h-8" style={{ background: `var(--status-${status})` }} />
      <div
        className="flex h-9 items-center px-2 text-xs font-medium"
        style={{ background: `var(--status-${status}-soft)`, color: `var(--status-${status}-fg)` }}
      >
        {status}
      </div>
      <figcaption className="bg-surface px-2 py-1 font-mono text-2xs text-muted-foreground">
        --status-{status}[-fg|-soft]
      </figcaption>
    </figure>
  );
}

/* ------------------------------------------------------------------------ */

/** Dev-only living style guide (see DESIGN_ROUTE_ENABLED in app/router.tsx). */
export function DesignPage() {
  return (
    <div className="mx-auto flex w-full max-w-(--content-max-width) flex-col gap-10 px-4 py-10 sm:px-8">
      <PageHeader
        eyebrow="Design system · dev only"
        title="Vault Ledger"
        description="Tokens and core components for bekup. Calm, precise and dense; ink on vellum by day, paper on vault slate by night. Status is always shape + color + text."
        actions={<ThemeToggle />}
      />

      <Section id="brand" title="Brand">
        <div className="flex flex-wrap items-center gap-8">
          <Wordmark />
          <LogoMark className="size-12" />
          <p className="max-w-sm text-sm text-muted-foreground">
            The mark is a vault dial. The wordmark is set in Instrument Serif; everything else in
            Instrument Sans, with JetBrains Mono for values an operator copies or compares.
          </p>
        </div>
      </Section>

      <Section id="color" title="Color">
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 xl:grid-cols-5">
          {SURFACE_TOKENS.map((t) => (
            <Swatch key={t} token={t} />
          ))}
        </div>
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 xl:grid-cols-6">
          {CHROME_TOKENS.map((t) => (
            <Swatch key={t} token={t} />
          ))}
        </div>
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4 xl:grid-cols-7">
          {STATUS_TOKENS.map((s) => (
            <StatusSwatch key={s} status={s} />
          ))}
        </div>
      </Section>

      <Section id="status" title="Status">
        <div className="flex flex-wrap gap-2">
          {RUN_STATUSES.map((s) => (
            <StatusBadge key={s} status={s} />
          ))}
        </div>
        <div className="flex flex-wrap items-center gap-4">
          {RUN_STATUSES.map((s) => (
            <span key={s} className="inline-flex items-center gap-2 text-sm">
              <StatusDot status={s} /> {s}
            </span>
          ))}
        </div>
        <div className="flex flex-wrap gap-2">
          {SECURITY_FLAGS.map((f) => (
            <SecurityBadge key={f} flag={f} />
          ))}
        </div>
        <p className="max-w-prose text-sm text-muted-foreground">
          The hazard hatch is reserved for weakened security settings. They are permanent: a badge
          stays visible for as long as the setting is in effect.
        </p>
      </Section>

      <Section id="type" title="Typography">
        <div className="flex flex-col divide-y divide-rule">
          {TYPE_SCALE.map(([token, cls, sample]) => (
            <div key={token} className="grid items-baseline gap-2 py-3 sm:grid-cols-[10rem_1fr]">
              <code className="font-mono text-2xs text-muted-foreground">{token}</code>
              <span className={cls}>{sample}</span>
            </div>
          ))}
        </div>
      </Section>

      <Section id="space" title="Space, radius, depth">
        <div className="flex flex-wrap items-end gap-4">
          {SPACING.map((n) => (
            <div key={n} className="flex flex-col items-center gap-1.5">
              <div
                className="bg-primary/70"
                style={{
                  width: `calc(var(--spacing) * ${n})`,
                  height: `calc(var(--spacing) * ${n})`,
                }}
              />
              <span className="font-mono text-2xs text-muted-foreground">{n * 4}px</span>
            </div>
          ))}
        </div>
        <div className="flex flex-wrap gap-4">
          {RADII.map((r) => (
            <div
              key={r}
              className={`flex size-20 items-end border-2 border-border-strong p-1.5 ${r}`}
            >
              <span className="font-mono text-2xs text-muted-foreground">
                {r.replace("rounded-", "")}
              </span>
            </div>
          ))}
        </div>
        <div className="flex flex-wrap gap-6">
          <div className="flex h-20 w-40 items-end rounded-lg bg-surface p-2 shadow-raised">
            <span className="font-mono text-2xs text-muted-foreground">shadow-raised</span>
          </div>
          <div className="flex h-20 w-40 items-end rounded-lg bg-popover p-2 shadow-overlay">
            <span className="font-mono text-2xs text-muted-foreground">shadow-overlay</span>
          </div>
        </div>
      </Section>

      <Section id="buttons" title="Buttons">
        <div className="flex flex-wrap items-center gap-2">
          <Button>
            <Play aria-hidden="true" /> Run now
          </Button>
          <Button variant="secondary">Secondary</Button>
          <Button variant="outline">
            <Download aria-hidden="true" /> Download
          </Button>
          <Button variant="ghost">Ghost</Button>
          <Button variant="link">Link</Button>
          <Button variant="destructive">
            <Trash2 aria-hidden="true" /> Delete
          </Button>
          <Button disabled>Disabled</Button>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button size="xs">xs</Button>
          <Button size="sm">sm</Button>
          <Button>default</Button>
          <Button size="lg">lg</Button>
          <Button size="icon" aria-label="Restore">
            <RotateCcw aria-hidden="true" />
          </Button>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Badge>Default</Badge>
          <Badge variant="secondary">Secondary</Badge>
          <Badge variant="outline">PostgreSQL 17</Badge>
          <Badge variant="destructive">Destructive</Badge>
        </div>
      </Section>

      <Section id="forms" title="Form controls">
        <div className="grid max-w-2xl gap-5 sm:grid-cols-2">
          <div className="flex flex-col gap-2">
            <Label htmlFor="ds-name">Job name</Label>
            <Input id="ds-name" placeholder="billing-pg-hourly" />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="ds-bad">Cron expression</Label>
            <Input
              id="ds-bad"
              defaultValue="61 * * * *"
              aria-invalid="true"
              aria-describedby="ds-bad-msg"
            />
            <p id="ds-bad-msg" className="text-xs text-destructive">
              Minute must be between 0 and 59.
            </p>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="ds-engine">Engine</Label>
            <Select defaultValue="postgres">
              <SelectTrigger id="ds-engine" className="w-full" data-testid="ds-select">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="postgres">PostgreSQL</SelectItem>
                <SelectItem value="mysql">MySQL</SelectItem>
                <SelectItem value="mariadb">MariaDB</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex items-center gap-3 self-end pb-2">
            <Switch id="ds-enc" defaultChecked />
            <Label htmlFor="ds-enc">Encrypt backups</Label>
          </div>
        </div>
      </Section>

      <Section id="overlays" title="Overlays">
        <div className="flex flex-wrap items-center gap-2">
          <Dialog>
            <DialogTrigger asChild>
              <Button variant="outline" data-testid="ds-dialog">
                Open dialog
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Restore to billing?</DialogTitle>
                <DialogDescription>
                  This overwrites the database <span className="font-mono">billing</span>. Type its
                  name to confirm.
                </DialogDescription>
              </DialogHeader>
              <Input aria-label="Database name" placeholder="billing" />
              <DialogFooter>
                <DialogClose asChild>
                  <Button variant="outline">Cancel</Button>
                </DialogClose>
                <Button variant="destructive">Restore</Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" data-testid="ds-menu">
                Actions <ChevronDown aria-hidden="true" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuLabel>Run 4821</DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuItem>Download (encrypted)</DropdownMenuItem>
              <DropdownMenuItem>Download (ready to use)</DropdownMenuItem>
              <DropdownMenuItem variant="destructive">Cancel run</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" data-testid="ds-tooltip">
                Hover for tooltip
              </Button>
            </TooltipTrigger>
            <TooltipContent>sha256 9f2c…e41a</TooltipContent>
          </Tooltip>
          <Button
            variant="secondary"
            data-testid="ds-toast"
            onClick={() => {
              toast.success("Backup finished", { description: "billing-pg-hourly · 1.82 GiB" });
            }}
          >
            Show toast
          </Button>
          <Button
            variant="secondary"
            onClick={() => {
              toast.error("Backup failed", { description: "legacy-maria-daily · exit status 2" });
            }}
          >
            Error toast
          </Button>
        </div>
      </Section>

      <Section id="data" title="Data display">
        <Tabs defaultValue="runs">
          <TabsList>
            <TabsTrigger value="runs">Runs</TabsTrigger>
            <TabsTrigger value="cards">Cards</TabsTrigger>
            <TabsTrigger value="loading">Loading</TabsTrigger>
          </TabsList>
          <TabsContent value="runs" className="mt-4">
            <div className="rounded-lg border bg-surface">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Job</TableHead>
                    <TableHead>Databases</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead className="text-right">Size</TableHead>
                    <TableHead>Started</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {SAMPLE_RUNS.map((r) => (
                    <TableRow key={r.job}>
                      <TableCell className="font-medium">{r.job}</TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {r.db}
                      </TableCell>
                      <TableCell>
                        <StatusBadge status={r.status} />
                      </TableCell>
                      <TableCell className="text-right font-mono text-xs">{r.size}</TableCell>
                      <TableCell>
                        <time className="font-mono text-xs text-muted-foreground">{r.at}</time>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </TabsContent>
          <TabsContent value="cards" className="mt-4">
            <div className="grid gap-4 md:grid-cols-2">
              <Card>
                <CardHeader>
                  <CardTitle>crm-mysql-nightly</CardTitle>
                  <CardDescription>
                    MySQL 8.4 · 2 databases · daily 02:00 Europe/Amsterdam
                  </CardDescription>
                </CardHeader>
                <CardContent className="flex flex-wrap gap-2">
                  <StatusBadge status="partial" label="Partial · 1 of 2" />
                  <SecurityBadge flag="unencrypted" />
                </CardContent>
                <CardFooter className="gap-2">
                  <Button size="sm">Run now</Button>
                  <Button size="sm" variant="outline">
                    History
                  </Button>
                </CardFooter>
              </Card>
              <Card>
                <CardHeader>
                  <CardTitle>legacy-maria-daily</CardTitle>
                  <CardDescription>MariaDB 10.11 · via SSH bastion</CardDescription>
                </CardHeader>
                <CardContent className="flex flex-wrap gap-2">
                  <StatusBadge status="failed" />
                  <SecurityBadge flag="insecure-transport" />
                </CardContent>
              </Card>
            </div>
          </TabsContent>
          <TabsContent value="loading" className="mt-4">
            <div className="flex flex-col gap-3">
              <Skeleton className="h-6 w-1/3" />
              <Skeleton className="h-4 w-full" />
              <Skeleton className="h-4 w-5/6" />
            </div>
          </TabsContent>
        </Tabs>
      </Section>

      <Section id="feedback" title="Alerts">
        <Alert>
          <AlertTitle>Certificate expires in 12 days</AlertTitle>
          <AlertDescription>
            The pinned certificate for pg-primary must be re-confirmed after rotation.
          </AlertDescription>
        </Alert>
        <Alert variant="destructive">
          <AlertTitle>Restore failed</AlertTitle>
          <AlertDescription>
            The manifest signature did not verify. Nothing was changed.
          </AlertDescription>
        </Alert>
      </Section>

      <Section id="nav" title="Navigation per role">
        <div className="grid gap-4 md:grid-cols-3">
          {ROLES.map((role) => (
            <Card key={role} className="gap-3">
              <CardHeader>
                <CardTitle className="eyebrow text-foreground">{role}</CardTitle>
              </CardHeader>
              <CardContent className="flex flex-col gap-3">
                {navForRole(role).map((s) => (
                  <div key={s.label}>
                    <p className="text-xs font-medium">{s.label}</p>
                    <p className="text-xs text-muted-foreground">
                      {s.items.map((i) => i.label).join(" · ")}
                    </p>
                  </div>
                ))}
              </CardContent>
            </Card>
          ))}
        </div>
      </Section>
    </div>
  );
}
