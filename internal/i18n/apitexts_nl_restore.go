package i18n

// apiNLRestore holds the Dutch API texts of this area (see apiNL).
var apiNLRestore = map[string]string{
	"%s is a system database of %s and cannot be a restore target":                 "%s is een systeemdatabase van %s en kan geen hersteldoel zijn",
	"1 to 63 characters: letters, digits, _ $ . - (not starting with - $ or .)":    "1 tot 63 tekens: letters, cijfers, _ $ . - (niet beginnend met - $ of .)",
	"a database name is required for database backups and not allowed for globals": "een databasenaam is verplicht bij database-back-ups en niet toegestaan bij globale objecten",
	"an identity is only needed for recipient-encrypted backups":                   "een identiteit is alleen nodig voor back-ups die voor een ontvanger zijn versleuteld",
	"artifactId and targetConnectionId are required":                               "artifactId en targetConnectionId zijn verplicht",
	"globals backups restore into the cluster, not into a database":                "back-ups van globale objecten worden in het cluster hersteld, niet in een database",
	"globals restores are PostgreSQL only (backup: %s, target: %s)":                "globale objecten herstellen kan alleen bij PostgreSQL (back-up: %s, doel: %s)",
	"manifest files cannot be restored":                                            "manifestbestanden kun je niet herstellen",
	"must be 1 to 255 bytes":                                                       "moet 1 tot 255 bytes zijn",
	"names containing __bekup_restore_ or __bekup_old_ and names starting with bekup_test_ are reserved for bekup's temporary and test-restore databases": "namen met __bekup_restore_ of __bekup_old_ en namen die met bekup_test_ beginnen zijn gereserveerd voor de tijdelijke en testherstel-databases van bekup",
	"not a globals backup: use restoreArtifact":                                                            "geen back-up van globale objecten: gebruik restoreArtifact",
	"not a restore target for this backup (unknown connection, another engine, or a temporary connection)": "geen hersteldoel voor deze back-up (onbekende verbinding, een andere engine of een tijdelijke verbinding)",
	"only PostgreSQL restores can keep the object owners":                                                  "alleen bij herstel naar PostgreSQL blijven de eigenaren van objecten behouden",
	"required": "verplicht",
	"the backup is a %s dump but the target runs %s (MySQL and MariaDB are different engines)":                                          "de back-up is een %s-dump, maar het doel draait %s (MySQL en MariaDB zijn verschillende engines)",
	"the target connection's role is privileged or its privileges are unknown: acknowledge that SQL in the backup runs with its rights": "de rol van de doelverbinding is bevoorrecht of de rechten zijn onbekend: bevestig dat SQL in de back-up met die rechten wordt uitgevoerd",
	"the temporary connection expires in less than the restore timeout (%s): extend it first":                                           "de tijdelijke herstelverbinding verloopt binnen de maximale duur van een herstel (%s): verleng die eerst",
	"this is a globals backup: use restoreGlobals (admins only)":                                                                        "dit is een back-up van globale objecten: gebruik restoreGlobals (alleen beheerders)",
	"type the confirmation phrase exactly: %s":                                                                                          "typ de bevestigingszin precies over: %s",
	"type the name of the database to overwrite exactly":                                                                                "typ de naam van de database die je overschrijft precies over",
	"unknown restore mode": "onbekende herstelmodus",
}
