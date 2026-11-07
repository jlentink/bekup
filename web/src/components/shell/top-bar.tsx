import { Info, LogOut, Menu, UserRound } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";

import { Wordmark } from "@/components/brand/logo";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
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
import type { SessionUser } from "@/features/auth/session-model";
import { useSignOut } from "@/features/auth/use-sign-out";
import { NotificationBell } from "@/features/notifications/bell";
import { messages } from "@/lib/messages";

import { AboutDialog } from "./about-dialog";
import { SidebarNav } from "./sidebar";

function MobileNav({ user, onAbout }: { user: SessionUser; onAbout: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          className="lg:hidden"
          aria-label={messages.shell.openNavigation}
        >
          <Menu aria-hidden="true" />
        </Button>
      </DialogTrigger>
      <DialogContent className="top-0 left-0 flex h-dvh max-w-72 translate-x-0 translate-y-0 flex-col gap-6 overflow-y-auto rounded-none border-y-0 border-l-0 bg-sidebar p-5 sm:max-w-72">
        <DialogTitle asChild>
          <div>
            <Wordmark />
          </div>
        </DialogTitle>
        <DialogDescription className="sr-only">{messages.shell.mainNavigation}</DialogDescription>
        <SidebarNav
          role={user.role}
          onNavigate={() => {
            setOpen(false);
          }}
        />
        <div className="mt-auto border-t border-sidebar-border pt-3">
          <button
            type="button"
            className="flex h-8 w-full items-center gap-2.5 rounded-md px-3 text-[0.8125rem] font-medium text-sidebar-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
            onClick={() => {
              setOpen(false);
              onAbout();
            }}
          >
            <Info aria-hidden="true" className="size-4 shrink-0 opacity-80" />
            {messages.about.openDrawer}
          </button>
          <p className="mt-1 px-3 font-mono text-xs text-sidebar-muted-foreground">
            {messages.about.version(__BEKUP_VERSION__)}
          </p>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  const letters = parts.length > 1 ? [parts[0], parts[parts.length - 1]] : parts;
  return (
    letters
      .map((p) => p?.charAt(0) ?? "")
      .join("")
      .toUpperCase() || "?"
  );
}

function UserMenu({ user, onAbout }: { user: SessionUser; onAbout: () => void }) {
  const signOut = useSignOut();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" className="h-8 gap-2 px-2" aria-label={messages.shell.accountMenu}>
          <span
            aria-hidden="true"
            className="inline-flex size-6 items-center justify-center rounded-sm bg-primary font-mono text-2xs font-semibold text-primary-foreground"
          >
            {initials(user.displayName)}
          </span>
          <span className="hidden text-[0.8125rem] font-medium sm:inline">{user.displayName}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-60">
        <DropdownMenuLabel className="flex flex-col gap-0.5">
          <span className="font-medium">{user.displayName}</span>
          <span className="truncate font-mono text-xs font-normal text-muted-foreground">
            {user.email}
          </span>
          <span className="eyebrow mt-1">{messages.roles[user.role]}</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/profile">
            <UserRound aria-hidden="true" /> {messages.shell.profile}
          </Link>
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onAbout}>
          <Info aria-hidden="true" /> {messages.about.open}
        </DropdownMenuItem>
        <DropdownMenuItem
          onSelect={() => {
            void signOut();
          }}
        >
          <LogOut aria-hidden="true" /> {messages.common.signOut}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function TopBar({ user, title }: { user: SessionUser; title: string }) {
  const [about, setAbout] = useState(false);
  const openAbout = () => {
    setAbout(true);
  };
  return (
    <header className="sticky top-0 z-30 flex h-(--shell-topbar-height) items-center gap-3 border-b bg-background/90 px-4 backdrop-blur-sm sm:px-6">
      <MobileNav user={user} onAbout={openAbout} />
      <p className="truncate text-[0.8125rem] font-medium text-muted-foreground">{title}</p>
      <div className="ml-auto flex items-center gap-1.5">
        <ThemeToggle className="mr-1.5 hidden sm:inline-flex" />
        <NotificationBell />
        <UserMenu user={user} onAbout={openAbout} />
      </div>
      <AboutDialog open={about} onOpenChange={setAbout} role={user.role} />
    </header>
  );
}
