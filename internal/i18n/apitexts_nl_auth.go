package i18n

// apiNLAuth holds the Dutch API texts of this area (see apiNL).
//
//nolint:gosec // G101: texts that mention passwords, keys and tokens, not credentials
var apiNLAuth = map[string]string{
	"Passkeys are unavailable.":                                     "Passkeys zijn niet beschikbaar.",
	"Passkeys are unavailable: server.base_url is not a valid URL.": "Passkeys zijn niet beschikbaar: server.base_url is geen geldige URL.",
	"Passkeys need a domain name: server.base_url uses an IP address, which cannot be a WebAuthn relying party ID.":                                                      "Passkeys hebben een domeinnaam nodig: server.base_url gebruikt een IP-adres, en dat kan geen WebAuthn relying party ID zijn.",
	"Passkeys need a secure context: server.base_url must use https (or localhost). Use an authenticator app (TOTP) instead, or put bekup behind the TLS reverse proxy.": "Passkeys hebben een beveiligde context nodig: server.base_url moet https (of localhost) gebruiken. Gebruik in plaats daarvan een authenticator-app (TOTP), of zet bekup achter de TLS reverse proxy.",
	"a fresh passkey or authenticator code check is required":                                                                                                            "een nieuwe controle met een passkey of een code uit je authenticator-app is nodig",
	"authentication required":                               "je moet inloggen",
	"change your temporary password first":                  "wijzig eerst je tijdelijke wachtwoord",
	"complete the sign-in with your second factor first":    "rond eerst het inloggen af met je tweede factor",
	"enter a display name of 1 to 200 characters":           "vul een weergavenaam van 1 tot 200 tekens in",
	"enter a valid e-mail address":                          "vul een geldig e-mailadres in",
	"enter a valid e-mail address (at most 254 characters)": "vul een geldig e-mailadres in (hooguit 254 tekens)",
	"not allowed":                                                                          "niet toegestaan",
	"please confirm your identity again":                                                   "bevestig je identiteit opnieuw",
	"set up a passkey or authenticator app first":                                          "stel eerst een passkey of authenticator-app in",
	"the display name must not contain control characters":                                 "de weergavenaam mag geen stuurtekens bevatten",
	"the new password must differ from the current one":                                    "het nieuwe wachtwoord moet anders zijn dan het huidige",
	"the passkey name must be at most %d characters":                                       "de naam van de passkey mag hooguit %d tekens lang zijn",
	"the passkey name must be valid UTF-8":                                                 "de naam van de passkey moet geldige UTF-8 zijn",
	"the passkey name must not be empty":                                                   "de naam van de passkey mag niet leeg zijn",
	"the passkey name must not contain control characters":                                 "de naam van de passkey mag geen stuurtekens bevatten",
	"the password contains invalid characters":                                             "het wachtwoord bevat ongeldige tekens",
	"the password is too easy to guess; use a longer passphrase or more varied characters": "het wachtwoord is te makkelijk te raden; gebruik een langere wachtwoordzin of meer verschillende tekens",
	"the password is too long (at most %d characters)":                                     "het wachtwoord is te lang (hooguit %d tekens)",
	"the password is too short (at least %d characters)":                                   "het wachtwoord is te kort (minstens %d tekens)",
	"the password must not contain control characters":                                     "het wachtwoord mag geen stuurtekens bevatten",
	"the password must not contain your e-mail address or name":                            "het wachtwoord mag je e-mailadres of naam niet bevatten",
	"this password is too common; choose another one":                                      "dit wachtwoord komt te vaak voor; kies een ander",
	"two-factor authentication is required: set up a passkey or authenticator app first":   "tweestapsverificatie is verplicht: stel eerst een passkey of authenticator-app in",
	"unknown role": "onbekende rol",
}
