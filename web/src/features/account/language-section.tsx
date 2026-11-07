import { Languages } from "lucide-react";
import { useId } from "react";

import { NativeSelect } from "@/components/ui/native-select";
import { Label } from "@/components/ui/label";
import { LOCALES, LOCALE_NAMES, hasStoredLocale, isLocale, locale, setLocale } from "@/lib/i18n";
import { messages } from "@/lib/messages";

import { Section } from "./section";

const M = messages.profile;
const BROWSER = "browser";

/**
 * The UI language of this browser (D-235): an explicit choice or the
 * browser's own languages. Changing it reloads the page.
 */
export function LanguageSection() {
  const id = useId();
  const value = hasStoredLocale() ? locale : BROWSER;
  return (
    <Section icon={Languages} title={M.languageHeading} lead={M.languageLead}>
      <div className="flex max-w-sm flex-col gap-2">
        <Label htmlFor={id}>{M.languageLabel}</Label>
        <NativeSelect
          id={id}
          value={value}
          onChange={(e) => {
            const next = e.target.value;
            setLocale(isLocale(next) ? next : null);
          }}
        >
          <option value={BROWSER}>{M.languageBrowser}</option>
          {LOCALES.map((l) => (
            <option key={l} value={l} lang={l}>
              {LOCALE_NAMES[l]}
            </option>
          ))}
        </NativeSelect>
      </div>
    </Section>
  );
}
