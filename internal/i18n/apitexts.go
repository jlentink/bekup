package i18n

import "maps"

// apiNL is the central catalog of the API texts (D-238): English format →
// Dutch format. The key is the exact English format a constructor got
// (i18n.M, i18n.Errorf, apierror.New/Newf/Invalid/Invalidf,
// serviceutil.FieldErr and the functions that forward to them);
// TestAPITextsHaveDutch fails for a format without an entry and for an
// unused entry, TestAPICatalogParity for differing formatting verbs.
//
// The entries live in apitexts_nl_*.go, one file per area, merged here.
// Dutch follows web/src/lib/i18n-glossary.md.
var apiNL = mergeTexts(
	apiNLGraph,
	apiNLAuth,
	apiNLConnections,
	apiNLDestinations,
	apiNLJobs,
	apiNLNotify,
	apiNLRestore,
	apiNLOther,
)

func mergeTexts(parts ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, p := range parts {
		for k, v := range p {
			if _, dup := out[k]; dup {
				panic("i18n: duplicate API text " + k)
			}
			out[k] = v
		}
	}
	return out
}

// APICatalog returns the API texts as a Catalog (English as written, Dutch
// from apiNL), for Problems.
func APICatalog() Catalog {
	en := make(map[string]string, len(apiNL))
	for k := range apiNL {
		en[k] = k
	}
	return Catalog{EN: en, NL: maps.Clone(apiNL)}
}
