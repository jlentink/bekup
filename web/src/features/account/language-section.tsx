import { useMutation } from "@apollo/client/react";
import { Languages } from "lucide-react";
import { useId, useState } from "react";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { NativeSelect } from "@/components/ui/native-select";
import { reportActionError } from "@/features/auth/use-step-up";
import { useSessionUser } from "@/features/auth/use-session-user";
import {
  LOCALES,
  LOCALE_NAMES,
  browserLocale,
  canStoreLocale,
  isLocale,
  isReloadPending,
  locale,
  localeFromApi,
  localeToApi,
  setLocale,
  storedFromAccount,
  storedLocale,
  type Locale,
} from "@/lib/i18n";
import { messages } from "@/lib/messages";

import { SetMyLocaleMutation } from "./operations";
import { Section } from "./section";

const M = messages.profile;
const BROWSER = "browser";

/**
 * The UI language (D-235, D-236): saved on the account so it follows the
 * user to every browser, or "same as this browser". Shows the account's
 * choice, else a choice made in this browser alone. Changing it saves it,
 * then reloads the page when the language changes.
 */
export function LanguageSection() {
  const id = useId();
  const user = useSessionUser();
  const [saving, setSaving] = useState(false);
  const [setMyLocale] = useMutation(SetMyLocaleMutation);
  const account = localeFromApi(user.locale);
  const browserOnly = account === null && !storedFromAccount() ? storedLocale() : null;
  const shown = account ?? browserOnly;

  const choose = async (next: Locale | null) => {
    setSaving(true);
    try {
      await setMyLocale({ variables: { locale: next ? localeToApi(next) : null } });
    } catch (err) {
      reportActionError(err);
      setSaving(false);
      return;
    }
    // The account sync (RequireSession) may already have started the reload.
    if (isReloadPending()) return;
    const resulting = next ?? browserLocale();
    const reload = resulting !== locale;
    if (!setLocale(next, { fromAccount: next !== null, reload }) || !reload) setSaving(false);
  };

  return (
    <Section icon={Languages} title={M.languageHeading} lead={M.languageLead}>
      <div className="flex max-w-sm flex-col gap-2">
        <Label htmlFor={id}>{M.languageLabel}</Label>
        <NativeSelect
          id={id}
          value={shown ?? BROWSER}
          disabled={saving}
          onChange={(e) => {
            const value = e.target.value;
            void choose(isLocale(value) ? value : null);
          }}
        >
          <option value={BROWSER}>{M.languageBrowser}</option>
          {LOCALES.map((l) => (
            <option key={l} value={l} lang={l}>
              {LOCALE_NAMES[l]}
            </option>
          ))}
        </NativeSelect>
        {browserOnly ? (
          <div className="flex flex-wrap items-center gap-2">
            <p className="text-xs text-muted-foreground">{M.languageBrowserOnly}</p>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={saving}
              onClick={() => {
                void choose(browserOnly);
              }}
            >
              {M.languageSaveForAccount}
            </Button>
          </div>
        ) : null}
        {canStoreLocale ? null : (
          <p className="text-xs text-muted-foreground">{M.languageNoStorage}</p>
        )}
      </div>
    </Section>
  );
}
