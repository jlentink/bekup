import { useQuery } from "@apollo/client/react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { userMessage } from "@/lib/api-error";
import { messages } from "@/lib/messages";

import { TwoFactorOverviewQuery } from "./operations";
import { PasskeysSection } from "./passkeys-section";
import { RecoveryCodesSection } from "./recovery-codes";
import { TotpSection } from "./totp-section";

/**
 * All second factors of the own account: passkeys, the authenticator app
 * and recovery codes. Used on the profile and by the forced enrollment.
 */
export function TwoFactorPanel({
  account,
  onFactorAdded,
  showRecovery = true,
}: {
  account: string;
  /** Called after a factor was added (and new recovery codes were acknowledged). */
  onFactorAdded?: () => void;
  showRecovery?: boolean;
}) {
  const { data, error, refetch } = useQuery(TwoFactorOverviewQuery, {
    fetchPolicy: "cache-and-network",
  });
  if (error && !data) {
    return (
      <Alert variant="destructive">
        <AlertDescription className="flex flex-wrap items-center gap-3">
          {userMessage(error)}
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              void refetch().catch(() => undefined);
            }}
          >
            {messages.common.retry}
          </Button>
        </AlertDescription>
      </Alert>
    );
  }
  if (!data) {
    return (
      <div className="flex flex-col gap-6" aria-busy="true">
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }
  const status = data.myTwoFactorStatus;
  const hasFactor = status.totpEnabled || data.myPasskeys.length > 0;
  return (
    <div className="flex flex-col">
      <PasskeysSection
        passkeys={data.myPasskeys}
        available={data.webauthnStatus.webauthnAvailable}
        reason={data.webauthnStatus.reason}
        {...(onFactorAdded ? { onAdded: onFactorAdded } : {})}
      />
      <TotpSection
        enabled={status.totpEnabled}
        enabledAt={status.totpEnabledAt}
        pending={status.totpEnrollmentPending}
        account={account}
        {...(onFactorAdded ? { onEnabled: onFactorAdded } : {})}
      />
      {showRecovery ? (
        <RecoveryCodesSection
          remaining={status.recoveryCodesRemaining}
          hasFactor={hasFactor}
          account={account}
        />
      ) : null}
    </div>
  );
}
