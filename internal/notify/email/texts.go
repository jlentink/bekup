package email

import "github.com/jlentink/bekup/internal/i18n"

// layoutTexts are the layout's own texts (D-237).
var layoutTexts = i18n.Catalog{
	i18n.EN: {
		"footer":         "This message was sent automatically by bekup. It contains no images, no tracking and no secrets.",
		"button_missing": "If the button does not work, copy this address into your browser:",
	},
	i18n.NL: {
		"footer":         "Dit bericht is automatisch verstuurd door bekup. Het bevat geen afbeeldingen, geen tracking en geen geheimen.",
		"button_missing": "Werkt de knop niet? Kopieer dan dit adres naar je browser:",
	},
}

// mailTexts are the texts of the e-mails this package sends itself
// (verification and the SMTP test; D-237).
var mailTexts = i18n.Catalog{
	i18n.EN: {
		"verify.subject": "Verify your e-mail address for bekup",
		"verify.heading": "Verify your e-mail address",
		"verify.p1":      "Someone (hopefully you) asked bekup to send notifications to this address. Open the link below while signed in to bekup to confirm it.",
		"verify.p2":      "The link works once and expires in 24 hours. If you did not ask for this, ignore this message: nothing will be sent to this address.",
		"verify.action":  "Verify e-mail address",
		"test.subject":   "bekup test e-mail",
		"test.heading":   "Your SMTP settings work",
		"test.p1":        "This is a test message from bekup. An administrator sent it to check the SMTP settings.",
		"test.p2":        "No action is needed.",
		"test.sent_at":   "Sent at",
	},
	i18n.NL: {
		"verify.subject": "Bevestig je e-mailadres voor bekup",
		"verify.heading": "Bevestig je e-mailadres",
		"verify.p1":      "Iemand (hopelijk jij) heeft bekup gevraagd meldingen naar dit adres te sturen. Open de link hieronder terwijl je bij bekup bent ingelogd om dit te bevestigen.",
		"verify.p2":      "De link werkt één keer en verloopt na 24 uur. Heb je hier niet om gevraagd? Negeer dit bericht dan: er wordt niets naar dit adres gestuurd.",
		"verify.action":  "E-mailadres bevestigen",
		"test.subject":   "Test-e-mail van bekup",
		"test.heading":   "Je SMTP-instellingen werken",
		"test.p1":        "Dit is een testbericht van bekup. Een beheerder heeft het verstuurd om de SMTP-instellingen te controleren.",
		"test.p2":        "Je hoeft niets te doen.",
		"test.sent_at":   "Verstuurd op",
	},
}
