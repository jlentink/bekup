import { AlertTriangle, Lightbulb, ShieldCheck, ShieldQuestion } from "lucide-react";
import { useState } from "react";

import { Tag } from "@/components/status/tag";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Fact, Facts } from "@/features/connections/ui";
import type { WebdavFlavor } from "@/graphql/gen/graphql";

import type { DestinationTestResult } from "./results";
import { CertificateFacts, PinCertificateDialog } from "./webdav-certificate";
import { TRUST_TONE } from "./webdav-model";
import { OVERWRITE_LABEL, SERVER_LABEL, TRUST_LABEL, UPLOAD_LABEL, WT } from "./webdav-text";

export type WebdavDetails = NonNullable<DestinationTestResult["webdav"]>;

/** Who looks at the test result: pin actions and the flavor hint are admin-only. */
export interface WebdavViewer {
  isAdmin: boolean;
  /** The destination's flavor (admins only: maintainers get no settings). */
  flavor?: WebdavFlavor | undefined;
}

/**
 * The WebDAV findings of a test (D-222): the presented certificate and
 * whether it is trusted or pinned, the recognized server, the upload mode
 * and the overwrite probe. Admins can pin a certificate that is not
 * already the matching pin.
 */
export function WebdavTestDetailsView({
  id,
  details,
  viewer,
}: {
  id: string;
  details: WebdavDetails;
  viewer: WebdavViewer;
}) {
  const [pinning, setPinning] = useState(false);
  const [pinned, setPinned] = useState(false);
  const cert = details.certificate;
  const isPinMatch = details.trust === "PINNED" && details.pinMatches;
  const canPin = viewer.isAdmin && cert != null && !isPinMatch && !pinned;
  const flavorHint =
    viewer.flavor !== undefined && details.suggestedFlavor !== viewer.flavor
      ? details.suggestedFlavor === "NEXTCLOUD"
        ? WT.test.suggestFlavor(details.server ? SERVER_LABEL[details.server] : "Nextcloud")
        : WT.test.suggestGeneric
      : undefined;

  return (
    <section
      aria-label={WT.test.heading}
      data-testid="webdav-test-details"
      className="flex flex-col gap-3 border-t border-rule pt-3"
    >
      <p className="eyebrow">{WT.test.heading}</p>
      <Facts>
        <Fact term={WT.test.trust}>
          <span className="flex flex-wrap items-center gap-1.5">
            <Tag tone={TRUST_TONE[details.trust]}>{TRUST_LABEL[details.trust]}</Tag>
            {cert ? (
              <Tag tone={details.trusted ? "success" : "danger"}>
                {details.trusted ? WT.test.trusted : WT.test.untrusted}
              </Tag>
            ) : null}
            {details.trust === "PINNED" && cert ? (
              <Tag tone={details.pinMatches ? "success" : "danger"}>
                {details.pinMatches ? WT.test.pinMatches : WT.test.pinMismatch}
              </Tag>
            ) : null}
          </span>
        </Fact>
        <Fact term={WT.test.server}>
          {details.server ? SERVER_LABEL[details.server] : WT.test.serverUnknown}
        </Fact>
        <Fact term={WT.test.uploadMode}>
          {details.uploadMode ? UPLOAD_LABEL[details.uploadMode] : WT.test.notRun}
        </Fact>
        <Fact term={WT.test.overwrite}>
          {details.overwriteProtection ? (
            <Tag tone={details.overwriteProtection === "REFUSED" ? "success" : "danger"}>
              {OVERWRITE_LABEL[details.overwriteProtection]}
            </Tag>
          ) : (
            WT.test.notRun
          )}
        </Fact>
      </Facts>

      {details.overwriteProtection === "IGNORED" ? (
        <Alert variant="destructive" data-testid="overwrite-ignored">
          <AlertTriangle aria-hidden="true" />
          <AlertTitle>{WT.test.ignoredTitle}</AlertTitle>
          <AlertDescription>{WT.test.ignoredBody}</AlertDescription>
        </Alert>
      ) : null}

      {flavorHint ? (
        <Alert>
          <Lightbulb aria-hidden="true" />
          <AlertDescription>{flavorHint}</AlertDescription>
        </Alert>
      ) : null}

      <div className="flex flex-col gap-1.5">
        <p className="eyebrow">{WT.test.certificate}</p>
        {cert ? (
          <CertificateFacts cert={cert} plateTestId="test-spki" />
        ) : (
          <p className="text-sm text-muted-foreground">{WT.test.noCertificate}</p>
        )}
      </div>

      {cert && !details.trusted && !pinned ? (
        <p className="flex items-start gap-2 text-sm">
          <ShieldQuestion
            aria-hidden="true"
            className="mt-0.5 size-4 shrink-0 text-status-failed"
          />
          <span>
            {details.trust === "PINNED" && !details.pinMatches
              ? WT.test.mismatchHint
              : WT.test.untrustedHint}
          </span>
        </p>
      ) : null}

      {pinned ? (
        <p role="status" className="flex items-start gap-2 text-sm">
          <ShieldCheck aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-status-success" />
          {WT.test.pinnedNow}
        </p>
      ) : null}

      {canPin ? (
        <>
          <Button
            type="button"
            size="sm"
            variant={details.trusted ? "outline" : "default"}
            className="self-start"
            onClick={() => {
              setPinning(true);
            }}
          >
            <ShieldCheck aria-hidden="true" /> {WT.test.pin}
          </Button>
          <PinCertificateDialog
            id={id}
            cert={cert}
            open={pinning}
            onOpenChange={setPinning}
            onPinned={() => {
              setPinned(true);
            }}
          />
        </>
      ) : null}
    </section>
  );
}
