import { ChevronLeft, ChevronRight } from "lucide-react";

import { Button } from "@/components/ui/button";
import { defineMessages } from "@/lib/i18n";

const T = defineMessages({
  en: {
    previous: "Previous",
    next: "Next",
    page: (n: number) => `Page ${String(n)}`,
  },
  nl: {
    previous: "Vorige",
    next: "Volgende",
    page: (n: number) => `Pagina ${String(n)}`,
  },
});

export function Pager({
  page,
  hasNext,
  onPrev,
  onNext,
  label,
}: {
  page: number;
  hasNext: boolean;
  onPrev: () => void;
  onNext: () => void;
  label: string;
}) {
  if (page === 1 && !hasNext) return null;
  return (
    <nav aria-label={label} className="flex items-center justify-end gap-2">
      <Button variant="outline" size="sm" disabled={page === 1} onClick={onPrev}>
        <ChevronLeft aria-hidden="true" /> {T.previous}
      </Button>
      <span className="font-mono text-xs text-muted-foreground">{T.page(page)}</span>
      <Button variant="outline" size="sm" disabled={!hasNext} onClick={onNext}>
        {T.next} <ChevronRight aria-hidden="true" />
      </Button>
    </nav>
  );
}
