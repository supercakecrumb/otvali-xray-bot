package telegram

import "strings"

// countryToFlag converts a country name to the corresponding flag emoji,
// or returns the default if no matching entry is found.
func countryToFlag(country string) string {
	isoCode, ok := countryNameToISO[strings.ToLower(country)]
	if !ok {
		// Return a default “null” or unknown flag
		return "🏳️"
	}
	return isoToFlag(isoCode)
}

// isoToFlag converts an ISO country code (e.g. "US") to the corresponding flag emoji.
func isoToFlag(isoCode string) string {
	isoCode = strings.ToUpper(isoCode)
	// Each letter is offset from 'A' + 127397
	// 'A' → '🇦'  ... 'Z' → '🇿'
	runes := []rune{}
	for _, char := range isoCode {
		// Convert char (A-Z) to its regional indicator code point
		runes = append(runes, rune(char-65+0x1F1E6))
	}
	return string(runes)
}

var countryNameToISO = map[string]string{
	"afghanistan":                            "AF",
	"åland islands":                          "AX",
	"albania":                                "AL",
	"algeria":                                "DZ",
	"american samoa":                         "AS",
	"andorra":                                "AD",
	"angola":                                 "AO",
	"anguilla":                               "AI",
	"antarctica":                             "AQ",
	"antigua and barbuda":                    "AG",
	"argentina":                              "AR",
	"armenia":                                "AM",
	"aruba":                                  "AW",
	"australia":                              "AU",
	"austria":                                "AT",
	"azerbaijan":                             "AZ",
	"bahamas":                                "BS",
	"bahrain":                                "BH",
	"bangladesh":                             "BD",
	"barbados":                               "BB",
	"belarus":                                "BY",
	"belgium":                                "BE",
	"belize":                                 "BZ",
	"benin":                                  "BJ",
	"bermuda":                                "BM",
	"bhutan":                                 "BT",
	"bolivia (plurinational state of)":       "BO",
	"bonaire, sint eustatius and saba":       "BQ",
	"bosnia and herzegovina":                 "BA",
	"botswana":                               "BW",
	"bouvet island":                          "BV",
	"brazil":                                 "BR",
	"british indian ocean territory":         "IO",
	"brunei darussalam":                      "BN",
	"bulgaria":                               "BG",
	"burkina faso":                           "BF",
	"burundi":                                "BI",
	"cabo verde":                             "CV",
	"cambodia":                               "KH",
	"cameroon":                               "CM",
	"canada":                                 "CA",
	"cayman islands":                         "KY",
	"central african republic":               "CF",
	"chad":                                   "TD",
	"chile":                                  "CL",
	"china":                                  "CN",
	"christmas island":                       "CX",
	"cocos (keeling) islands":                "CC",
	"colombia":                               "CO",
	"comoros":                                "KM",
	"congo":                                  "CG",
	"congo, democratic republic of the":      "CD",
	"cook islands":                           "CK",
	"costa rica":                             "CR",
	"côte d'ivoire":                          "CI",
	"croatia":                                "HR",
	"cuba":                                   "CU",
	"curaçao":                                "CW",
	"cyprus":                                 "CY",
	"czechia":                                "CZ",
	"denmark":                                "DK",
	"djibouti":                               "DJ",
	"dominica":                               "DM",
	"dominican republic":                     "DO",
	"ecuador":                                "EC",
	"egypt":                                  "EG",
	"el salvador":                            "SV",
	"equatorial guinea":                      "GQ",
	"eritrea":                                "ER",
	"estonia":                                "EE",
	"eswatini":                               "SZ",
	"ethiopia":                               "ET",
	"falkland islands (malvinas)":            "FK",
	"faroe islands":                          "FO",
	"fiji":                                   "FJ",
	"finland":                                "FI",
	"france":                                 "FR",
	"french guiana":                          "GF",
	"french polynesia":                       "PF",
	"french southern territories":            "TF",
	"gabon":                                  "GA",
	"gambia":                                 "GM",
	"georgia":                                "GE",
	"germany":                                "DE",
	"ghana":                                  "GH",
	"gibraltar":                              "GI",
	"greece":                                 "GR",
	"greenland":                              "GL",
	"grenada":                                "GD",
	"guadeloupe":                             "GP",
	"guam":                                   "GU",
	"guatemala":                              "GT",
	"guernsey":                               "GG",
	"guinea":                                 "GN",
	"guinea-bissau":                          "GW",
	"guyana":                                 "GY",
	"haiti":                                  "HT",
	"heard island and mcdonald islands":      "HM",
	"holy see":                               "VA",
	"honduras":                               "HN",
	"hong kong":                              "HK",
	"hungary":                                "HU",
	"iceland":                                "IS",
	"india":                                  "IN",
	"indonesia":                              "ID",
	"iran":                                   "IR",
	"iraq":                                   "IQ",
	"ireland":                                "IE",
	"isle of man":                            "IM",
	"israel":                                 "IL",
	"italy":                                  "IT",
	"jamaica":                                "JM",
	"japan":                                  "JP",
	"jersey":                                 "JE",
	"jordan":                                 "JO",
	"kazakhstan":                             "KZ",
	"kenya":                                  "KE",
	"kiribati":                               "KI",
	"korea, democratic people's republic of": "KP",
	"korea, republic of":                     "KR",
	"kuwait":                                 "KW",
	"kyrgyzstan":                             "KG",
	"lao people's democratic republic":       "LA",
	"latvia":                                 "LV",
	"lebanon":                                "LB",
	"lesotho":                                "LS",
	"liberia":                                "LR",
	"libya":                                  "LY",
	"liechtenstein":                          "LI",
	"lithuania":                              "LT",
	"luxembourg":                             "LU",
	"macao":                                  "MO",
	"madagascar":                             "MG",
	"malawi":                                 "MW",
	"malaysia":                               "MY",
	"maldives":                               "MV",
	"mali":                                   "ML",
	"malta":                                  "MT",
	"marshall islands":                       "MH",
	"martinique":                             "MQ",
	"mauritania":                             "MR",
	"mauritius":                              "MU",
	"mayotte":                                "YT",
	"mexico":                                 "MX",
	"micronesia (federated states of)":       "FM",
	"moldova, republic of":                   "MD",
	"monaco":                                 "MC",
	"mongolia":                               "MN",
	"montenegro":                             "ME",
	"montserrat":                             "MS",
	"morocco":                                "MA",
	"mozambique":                             "MZ",
	"myanmar":                                "MM",
	"namibia":                                "NA",
	"nauru":                                  "NR",
	"nepal":                                  "NP",
	"netherlands":                            "NL",
	"new caledonia":                          "NC",
	"new zealand":                            "NZ",
	"nicaragua":                              "NI",
	"niger":                                  "NE",
	"nigeria":                                "NG",
	"niue":                                   "NU",
	"norfolk island":                         "NF",
	"northern mariana islands":               "MP",
	"norway":                                 "NO",
	"oman":                                   "OM",
	"pakistan":                               "PK",
	"palau":                                  "PW",
	"palestine, state of":                    "PS",
	"panama":                                 "PA",
	"papua new guinea":                       "PG",
	"paraguay":                               "PY",
	"peru":                                   "PE",
	"philippines":                            "PH",
	"pitcairn":                               "PN",
	"poland":                                 "PL",
	"portugal":                               "PT",
	"puerto rico":                            "PR",
	"qatar":                                  "QA",
	"réunion":                                "RE",
	"romania":                                "RO",
	"russian federation":                     "RU",
	"russia":                                 "RU",
	"rwanda":                                 "RW",
	"saint barthélemy":                       "BL",
	"saint helena, ascension and tristan da cunha": "SH",
	"saint kitts and nevis":                        "KN",
	"saint lucia":                                  "LC",
	"saint martin (french part)":                   "MF",
	"saint pierre and miquelon":                    "PM",
	"saint vincent and the grenadines":             "VC",
	"samoa":                                        "WS",
	"san marino":                                   "SM",
	"sao tome and principe":                        "ST",
	"saudi arabia":                                 "SA",
	"senegal":                                      "SN",
	"serbia":                                       "RS",
	"seychelles":                                   "SC",
	"sierra leone":                                 "SL",
	"singapore":                                    "SG",
	"sint maarten (dutch part)":                    "SX",
	"slovakia":                                     "SK",
	"slovenia":                                     "SI",
	"solomon islands":                              "SB",
	"somalia":                                      "SO",
	"south africa":                                 "ZA",
	"south georgia and the south sandwich islands": "GS",
	"south sudan":                          "SS",
	"spain":                                "ES",
	"sri lanka":                            "LK",
	"sudan":                                "SD",
	"suriname":                             "SR",
	"svalbard and jan mayen":               "SJ",
	"sweden":                               "SE",
	"switzerland":                          "CH",
	"syrian arab republic":                 "SY",
	"taiwan":                               "TW",
	"tajikistan":                           "TJ",
	"tanzania, united republic of":         "TZ",
	"thailand":                             "TH",
	"timor-leste":                          "TL",
	"togo":                                 "TG",
	"tokelau":                              "TK",
	"tonga":                                "TO",
	"trinidad and tobago":                  "TT",
	"tunisia":                              "TN",
	"turkey":                               "TR",
	"turkmenistan":                         "TM",
	"turks and caicos islands":             "TC",
	"tuvalu":                               "TV",
	"uganda":                               "UG",
	"ukraine":                              "UA",
	"united arab emirates":                 "AE",
	"united kingdom":                       "GB",
	"uk":                                   "GB",
	"britain":                              "GB",
	"united states of america":             "US",
	"usa":                                  "US",
	"united states minor outlying islands": "UM",
	"uruguay":                              "UY",
	"uzbekistan":                           "UZ",
	"vanuatu":                              "VU",
	"venezuela (bolivarian republic of)":   "VE",
	"viet nam":                             "VN",
	"virgin islands (british)":             "VG",
	"virgin islands (u.s.)":                "VI",
	"wallis and futuna":                    "WF",
	"western sahara":                       "EH",
	"yemen":                                "YE",
	"zambia":                               "ZM",
	"zimbabwe":                             "ZW",
}
