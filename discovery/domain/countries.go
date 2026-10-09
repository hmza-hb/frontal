package domain

import (
	"sort"
	"strings"
)

// Country is a geographic discovery dimension.
//
// The Suffix field is the only thing that makes a country a first-class
// discovery dimension: it is the one piece of geography a web page states about
// itself without anyone having to fill in a form. Everything else here is the
// vocabulary needed to *ask* about that country in its own language, because
// "B2B SaaS companies" in German is "B2B-SaaS-Unternehmen" and searching the
// English phrase in Germany finds English-language US sites instead.
type Country struct {
	// Code is ISO 3166-1 alpha-2, always uppercase.
	Code string
	// Name is the English country name.
	Name string
	// Suffixes are the ccTLDs that imply this country. Empty for countries that
	// have none, which is rare but real.
	Suffixes []string
	// Cities are major business centres, used to build geographic queries.
	// A country with no city produces no geography-specific query, which is the
	// correct outcome rather than a generic one.
	Cities []string
	// Terms maps a BCP 47 language tag to that language's term for a business
	// software product. A missing language simply contributes no queries, so
	// adding a language never requires touching query generation.
	Terms map[string]string
	// Markets are the country names as they appear in search queries, including
	// the forms people actually type ("UK" not "United Kingdom").
	Markets []string
}

// HasLanguage reports whether the registry carries vocabulary for a language.
func (c Country) HasLanguage(lang string) bool {
	_, ok := c.Terms[normalizeLang(lang)]
	return ok
}

// Term returns the localized product term for a language, or "" when the
// registry carries no vocabulary for it.
//
// There is deliberately no fallback to English. A caller that asks for a
// Swahili term and receives the English one would file an English query under a
// Swahili provenance record, and every later stage would trust that record. The
// query builder decides how to handle a missing term; the registry only answers
// the question it was asked.
func (c Country) Term(lang string) string {
	return c.Terms[normalizeLang(lang)]
}

func normalizeLang(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	// Accept "en-GB" and "en_GB" as well as "en".
	if i := strings.IndexAny(l, "-_"); i > 0 {
		l = l[:i]
	}
	return l
}

// countries is the built-in registry. Adding a country is a data change, never
// a code change: nothing outside this file switches on a specific country.
var countries = buildCountries()

func buildCountries() map[string]Country {
	list := []Country{
		{
			Code: "US", Name: "United States",
			Suffixes: []string{".us"},
			Cities:   []string{"New York", "San Francisco", "Austin", "Boston", "Seattle", "Chicago", "Denver", "Los Angeles"},
			Markets:  []string{"US", "USA", "United States"},
			Terms: map[string]string{
				"en": "B2B SaaS", "es": "software B2B", "fr": "logiciel B2B",
			},
		},
		{
			Code: "GB", Name: "United Kingdom",
			Suffixes: []string{".uk", ".co.uk", ".org.uk", ".me.uk", ".ac.uk", ".gov.uk", ".ltd.uk", ".plc.uk", ".net.uk", ".sch.uk", ".nhs.uk"},
			Cities:   []string{"London", "Manchester", "Edinburgh", "Bristol", "Cambridge", "Leeds"},
			Markets:  []string{"UK", "United Kingdom", "Britain"},
			Terms: map[string]string{
				"en": "B2B SaaS", "cy": "meddalennau B2B", "fr": "logiciel B2B", "de": "B2B-Software",
			},
		},
		{
			Code: "IE", Name: "Ireland",
			Suffixes: []string{".ie", ".co.ie", ".org.ie"},
			Cities:   []string{"Dublin", "Cork", "Galway"},
			Markets:  []string{"Ireland", "Dublin"},
			Terms:    map[string]string{"en": "B2B SaaS", "ga": "software B2B"},
		},
		{
			Code: "DE", Name: "Germany",
			Suffixes: []string{".de"},
			Cities:   []string{"Berlin", "Munich", "Hamburg", "Frankfurt", "Cologne", "Stuttgart"},
			Markets:  []string{"Germany", "Deutschland", "German"},
			Terms: map[string]string{
				"en": "B2B SaaS", "de": "B2B-Software", "fr": "logiciel B2B",
			},
		},
		{
			Code: "FR", Name: "France",
			Suffixes: []string{".fr", ".co.fr", ".nom.fr", ".asso.fr"},
			Cities:   []string{"Paris", "Lyon", "Toulouse", "Marseille", "Lille", "Bordeaux"},
			Markets:  []string{"France", "Français", "French"},
			Terms: map[string]string{
				"en": "B2B SaaS", "fr": "logiciel B2B", "de": "B2B-Software", "es": "software B2B",
			},
		},
		{
			Code: "NL", Name: "Netherlands",
			Suffixes: []string{".nl", ".co.nl", ".org.nl"},
			Cities:   []string{"Amsterdam", "Rotterdam", "Utrecht", "Eindhoven"},
			Markets:  []string{"Netherlands", "Nederland", "Dutch"},
			Terms:    map[string]string{"en": "B2B SaaS", "nl": "B2B-software", "de": "B2B-Software"},
		},
		{
			Code: "ES", Name: "Spain",
			Suffixes: []string{".es", ".com.es", ".org.es", ".nom.es"},
			Cities:   []string{"Madrid", "Barcelona", "Valencia", "Seville", "Bilbao"},
			Markets:  []string{"Spain", "España", "Spanish"},
			Terms:    map[string]string{"en": "B2B SaaS", "es": "software B2B", "ca": "programari B2B"},
		},
		{
			Code: "IT", Name: "Italy",
			Suffixes: []string{".it", ".co.it"},
			Cities:   []string{"Milan", "Rome", "Turin", "Naples", "Bologna"},
			Markets:  []string{"Italy", "Italia", "Italian"},
			Terms:    map[string]string{"en": "B2B SaaS", "it": "software B2B"},
		},
		{
			Code: "PT", Name: "Portugal",
			Suffixes: []string{".pt", ".com.pt"},
			Cities:   []string{"Lisbon", "Porto", "Braga"},
			Markets:  []string{"Portugal", "Português", "Portuguese"},
			Terms:    map[string]string{"en": "B2B SaaS", "pt": "software B2B"},
		},
		{
			Code: "SE", Name: "Sweden",
			Suffixes: []string{".se", ".com.se", ".org.se"},
			Cities:   []string{"Stockholm", "Gothenburg", "Malmö"},
			Markets:  []string{"Sweden", "Sverige", "Swedish"},
			Terms:    map[string]string{"en": "B2B SaaS", "sv": "B2B-programvara"},
		},
		{
			Code: "DK", Name: "Denmark",
			Suffixes: []string{".dk", ".co.dk"},
			Cities:   []string{"Copenhagen", "Aarhus"},
			Markets:  []string{"Denmark", "Danmark", "Danish"},
			Terms:    map[string]string{"en": "B2B SaaS", "da": "B2B-software"},
		},
		{
			Code: "NO", Name: "Norway",
			Suffixes: []string{".no", ".co.no"},
			Cities:   []string{"Oslo", "Bergen", "Trondheim"},
			Markets:  []string{"Norway", "Norge", "Norwegian"},
			Terms:    map[string]string{"en": "B2B SaaS", "no": "B2B-programvare"},
		},
		{
			Code: "FI", Name: "Finland",
			Suffixes: []string{".fi"},
			Cities:   []string{"Helsinki", "Espoo", "Tampere"},
			Markets:  []string{"Finland", "Suomi", "Finnish"},
			Terms:    map[string]string{"en": "B2B SaaS", "fi": "B2B-ohjelmisto"},
		},
		{
			Code: "PL", Name: "Poland",
			Suffixes: []string{".pl", ".com.pl", ".co.pl"},
			Cities:   []string{"Warsaw", "Kraków", "Wrocław", "Gdańsk"},
			Markets:  []string{"Poland", "Polska", "Polish"},
			Terms:    map[string]string{"en": "B2B SaaS", "pl": "oprogramowanie B2B"},
		},
		{
			Code: "CZ", Name: "Czechia",
			Suffixes: []string{".cz", ".co.cz"},
			Cities:   []string{"Prague", "Brno"},
			Markets:  []string{"Czechia", "Czech Republic", "Česko", "Czech"},
			Terms:    map[string]string{"en": "B2B SaaS", "cs": "B2B software"},
		},
		{
			Code: "CH", Name: "Switzerland",
			Suffixes: []string{".ch"},
			Cities:   []string{"Zurich", "Geneva", "Basel", "Bern"},
			Markets:  []string{"Switzerland", "Schweiz", "Swiss"},
			Terms: map[string]string{
				"en": "B2B SaaS", "de": "B2B-Software", "fr": "logiciel B2B", "it": "software B2B",
			},
		},
		{
			Code: "AT", Name: "Austria",
			Suffixes: []string{".at", ".co.at"},
			Cities:   []string{"Vienna", "Graz", "Linz"},
			Markets:  []string{"Austria", "Österreich", "Austrian"},
			Terms:    map[string]string{"en": "B2B SaaS", "de": "B2B-Software"},
		},
		{
			Code: "CA", Name: "Canada",
			Suffixes: []string{".ca"},
			Cities:   []string{"Toronto", "Vancouver", "Montreal", "Calgary", "Ottawa"},
			Markets:  []string{"Canada", "Canadian"},
			Terms: map[string]string{
				"en": "B2B SaaS", "fr": "logiciel B2B",
			},
		},
		{
			Code: "AU", Name: "Australia",
			Suffixes: []string{".au", ".com.au", ".net.au", ".org.au", ".edu.au", ".gov.au", ".id.au"},
			Cities:   []string{"Sydney", "Melbourne", "Brisbane", "Perth", "Adelaide"},
			Markets:  []string{"Australia", "Australian"},
			Terms:    map[string]string{"en": "B2B SaaS"},
		},
		{
			Code: "NZ", Name: "New Zealand",
			Suffixes: []string{".nz", ".co.nz", ".net.nz", ".org.nz", ".govt.nz", ".ac.nz", ".school.nz"},
			Cities:   []string{"Auckland", "Wellington", "Christchurch"},
			Markets:  []string{"New Zealand", "NZ", "Kiwi"},
			Terms:    map[string]string{"en": "B2B SaaS", "mi": "software B2B"},
		},
		{
			Code: "SG", Name: "Singapore",
			Suffixes: []string{".sg", ".com.sg", ".org.sg", ".edu.sg", ".gov.sg"},
			Cities:   []string{"Singapore"},
			Markets:  []string{"Singapore", "Singaporean"},
			Terms: map[string]string{
				"en": "B2B SaaS", "zh": "企业软件", "ta": "அமைப்பு மென்பொருள்",
			},
		},
		{
			Code: "IN", Name: "India",
			Suffixes: []string{".in", ".co.in", ".net.in", ".org.in", ".firm.in", ".gen.in", ".ind.in", ".ac.in", ".edu.in", ".gov.in", ".res.in"},
			Cities:   []string{"Mumbai", "Bengaluru", "Delhi", "Hyderabad", "Chennai", "Pune", "Gurugram"},
			Markets:  []string{"India", "Indian"},
			Terms: map[string]string{
				"en": "B2B SaaS", "hi": "बी2बी सॉफ्टवेयर",
			},
		},
		{
			Code: "PK", Name: "Pakistan",
			Suffixes: []string{".pk", ".com.pk", ".net.pk", ".org.pk", ".edu.pk", ".gov.pk"},
			Cities:   []string{"Karachi", "Lahore", "Islamabad", "Faisalabad"},
			Markets:  []string{"Pakistan", "Pakistani"},
			Terms: map[string]string{
				"en": "B2B SaaS", "ur": "بی 2 بی سافٹ ویئر",
			},
		},
		{
			Code: "BD", Name: "Bangladesh",
			Suffixes: []string{".bd", ".com.bd", ".org.bd", ".gov.bd"},
			Cities:   []string{"Dhaka", "Chattogram"},
			Markets:  []string{"Bangladesh", "Bangladeshi"},
			Terms:    map[string]string{"en": "B2B SaaS", "bn": "ব্যবসায়িক সফটওয়্যার"},
		},
		{
			Code: "AE", Name: "United Arab Emirates",
			Suffixes: []string{".ae"},
			Cities:   []string{"Dubai", "Abu Dhabi", "Sharjah"},
			Markets:  []string{"UAE", "United Arab Emirates", "Dubai", "Emirates"},
			Terms: map[string]string{
				"en": "B2B SaaS", "ar": "برمجيات الأعمال",
			},
		},
		{
			Code: "IL", Name: "Israel",
			Suffixes: []string{".il", ".co.il", ".org.il", ".ac.il", ".net.il", ".gov.il"},
			Cities:   []string{"Tel Aviv", "Jerusalem", "Haifa"},
			Markets:  []string{"Israel", "Israeli"},
			Terms:    map[string]string{"en": "B2B SaaS", "he": "תוכנה לעסקים"},
		},
		{
			Code: "SA", Name: "Saudi Arabia",
			Suffixes: []string{".sa", ".com.sa", ".net.sa", ".org.sa", ".gov.sa", ".edu.sa"},
			Cities:   []string{"Riyadh", "Jeddah", "Dammam"},
			Markets:  []string{"Saudi Arabia", "Saudi", "KSA", "Riyadh"},
			Terms: map[string]string{
				"en": "B2B SaaS", "ar": "برمجيات الأعمال",
			},
		},
		{
			Code: "JP", Name: "Japan",
			Suffixes: []string{".jp", ".co.jp", ".ne.jp", ".or.jp", ".ac.jp", ".go.jp", ".lg.jp", ".ed.jp", ".gr.jp"},
			Cities:   []string{"Tokyo", "Osaka", "Kyoto", "Yokohama"},
			Markets:  []string{"Japan", "Japanese", "Tokyo"},
			Terms:    map[string]string{"en": "B2B SaaS", "ja": "法人向けソフトウェア"},
		},
		{
			Code: "KR", Name: "South Korea",
			Suffixes: []string{".kr", ".co.kr", ".ne.kr", ".or.kr", ".re.kr", ".pe.kr", ".go.kr"},
			Cities:   []string{"Seoul", "Busan", "Incheon"},
			Markets:  []string{"South Korea", "Korea", "Korean", "Seoul"},
			Terms:    map[string]string{"en": "B2B SaaS", "ko": "기업용 소프트웨어"},
		},
		{
			Code: "CN", Name: "China",
			Suffixes: []string{".cn", ".com.cn", ".net.cn", ".org.cn", ".gov.cn", ".edu.cn", ".ac.cn"},
			Cities:   []string{"Beijing", "Shanghai", "Shenzhen", "Hangzhou", "Guangzhou"},
			Markets:  []string{"China", "Chinese", "PRC"},
			Terms:    map[string]string{"en": "B2B SaaS", "zh": "企业软件"},
		},
		{
			Code: "TW", Name: "Taiwan",
			Suffixes: []string{".tw", ".com.tw", ".org.tw", ".gov.tw", ".edu.tw"},
			Cities:   []string{"Taipei", "Kaohsiung"},
			Markets:  []string{"Taiwan", "Taiwanese"},
			Terms:    map[string]string{"en": "B2B SaaS", "zh": "企業軟體"},
		},
		{
			Code: "HK", Name: "Hong Kong",
			Suffixes: []string{".hk", ".com.hk", ".org.hk", ".edu.hk", ".gov.hk"},
			Cities:   []string{"Hong Kong"},
			Markets:  []string{"Hong Kong", "HK"},
			Terms:    map[string]string{"en": "B2B SaaS", "zh": "企業軟件"},
		},
		{
			Code: "ZA", Name: "South Africa",
			Suffixes: []string{".za", ".co.za", ".org.za"},
			Cities:   []string{"Cape Town", "Johannesburg", "Durban"},
			Markets:  []string{"South Africa", "African"},
			Terms:    map[string]string{"en": "B2B SaaS", "af": "besigheid-sagteware"},
		},
		{
			Code: "NG", Name: "Nigeria",
			Suffixes: []string{".ng", ".com.ng", ".org.ng", ".gov.ng", ".edu.ng"},
			Cities:   []string{"Lagos", "Abuja", "Kano"},
			Markets:  []string{"Nigeria", "Nigerian", "Lagos"},
			Terms:    map[string]string{"en": "B2B SaaS"},
		},
		{
			Code: "KE", Name: "Kenya",
			Suffixes: []string{".ke", ".co.ke", ".or.ke", ".go.ke", ".ac.ke"},
			Cities:   []string{"Nairobi", "Mombasa"},
			Markets:  []string{"Kenya", "Kenyan", "Nairobi"},
			Terms:    map[string]string{"en": "B2B SaaS", "sw": "biashara programu"},
		},
		{
			Code: "BR", Name: "Brazil",
			Suffixes: []string{".br", ".com.br", ".net.br", ".org.br", ".gov.br", ".edu.br"},
			Cities:   []string{"São Paulo", "Rio de Janeiro", "Belo Horizonte", "Florianópolis"},
			Markets:  []string{"Brazil", "Brasil", "Brazilian"},
			Terms:    map[string]string{"en": "B2B SaaS", "pt": "software B2B"},
		},
		{
			Code: "MX", Name: "Mexico",
			Suffixes: []string{".mx", ".com.mx", ".org.mx", ".gob.mx", ".edu.mx"},
			Cities:   []string{"Mexico City", "Monterrey", "Guadalajara", "Querétaro"},
			Markets:  []string{"Mexico", "México", "Mexican"},
			Terms:    map[string]string{"en": "B2B SaaS", "es": "software empresarial"},
		},
		{
			Code: "AR", Name: "Argentina",
			Suffixes: []string{".ar", ".com.ar", ".net.ar", ".org.ar", ".gob.ar", ".edu.ar"},
			Cities:   []string{"Buenos Aires", "Córdoba", "Rosario"},
			Markets:  []string{"Argentina", "Argentinian"},
			Terms:    map[string]string{"en": "B2B SaaS", "es": "software B2B"},
		},
		{
			Code: "CL", Name: "Chile",
			Suffixes: []string{".cl", ".com.cl", ".gov.cl"},
			Cities:   []string{"Santiago", "Valparaíso"},
			Markets:  []string{"Chile", "Chilean"},
			Terms:    map[string]string{"en": "B2B SaaS", "es": "software B2B"},
		},
		{
			Code: "CO", Name: "Colombia",
			Suffixes: []string{".co", ".com.co", ".net.co", ".org.co", ".gov.co", ".edu.co"},
			Cities:   []string{"Bogotá", "Medellín", "Cali"},
			Markets:  []string{"Colombia", "Colombian"},
			Terms:    map[string]string{"en": "B2B SaaS", "es": "software B2B"},
		},
		{
			Code: "RU", Name: "Russia",
			Suffixes: []string{".ru", ".com.ru", ".net.ru", ".org.ru"},
			Cities:   []string{"Moscow", "Saint Petersburg", "Novosibirsk"},
			Markets:  []string{"Russia", "Russian", "Россия"},
			Terms:    map[string]string{"en": "B2B SaaS", "ru": "B2B программное обеспечение"},
		},
		{
			Code: "TR", Name: "Türkiye",
			Suffixes: []string{".tr", ".com.tr", ".net.tr", ".org.tr", ".gov.tr", ".edu.tr"},
			Cities:   []string{"Istanbul", "Ankara", "Izmir"},
			Markets:  []string{"Turkey", "Türkiye", "Turkish"},
			Terms:    map[string]string{"en": "B2B SaaS", "tr": "Kurumsal yazılım"},
		},
		{
			Code: "GR", Name: "Greece",
			Suffixes: []string{".gr", ".com.gr", ".org.gr", ".gov.gr", ".edu.gr"},
			Cities:   []string{"Athens", "Thessaloniki"},
			Markets:  []string{"Greece", "Greek"},
			Terms:    map[string]string{"en": "B2B SaaS", "el": "λογισμικό επιχειρήσεων"},
		},
	}

	// The registry is keyed by uppercase alpha-2 code. A malformed entry is a
	// programming error caught here rather than a silent lookup miss at query
	// time, which is much harder to diagnose.
	out := make(map[string]Country, len(list))
	for _, c := range list {
		if c.Name == "" || len(c.Code) != 2 {
			// A malformed entry is a bug in this file. Dropping it keeps the
			// engine running; the test that counts entries catches the loss.
			continue
		}
		c.Code = strings.ToUpper(c.Code)
		out[c.Code] = c
	}
	return out
}

// LookupCountry resolves a country by ISO 3166-1 alpha-2 code. Matching is
// case-insensitive and tolerates surrounding whitespace because these codes
// arrive from ICP files written by hand.
func LookupCountry(code string) (Country, bool) {
	c, ok := countries[strings.ToUpper(strings.TrimSpace(code))]
	return c, ok
}

// CountryByName resolves a country by its English name or a market alias, which
// is how an ICP file usually names it ("Germany", "UK", "United States").
func LookupCountryByName(name string) (Country, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return Country{}, false
	}
	if c, ok := LookupCountry(n); ok {
		return c, true
	}
	for _, c := range countries {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
		for _, m := range c.Markets {
			if strings.EqualFold(m, name) {
				return c, true
			}
		}
	}
	return Country{}, false
}

// Countries returns every country in the registry, sorted by name.
func Countries() []Country {
	out := make([]Country, 0, len(countries))
	for _, c := range countries {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// countryFromSuffix maps a public suffix to a country code.
//
// It walks the suffix from longest to shortest, because a country registers
// second-level domains under its ccTLD: ".com.ar" is Argentina, but so is
// ".co.za" under South Africa and ".co.uk" under the United Kingdom, and the
// two-label form is not in either country's suffix list verbatim.
//
// A .uk domain identifies a United Kingdom domain, not a company headquartered
// there, which is why the result is carried through provenance as a hint and
// never asserted as a fact about the business.
func countryFromSuffix(suffix string) string {
	s := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(suffix), "."))
	if s == "" {
		return ""
	}
	// Try the full suffix, then progressively shorter tails. The registry is
	// consulted first so a registry entry always wins over the generic table.
	labels := strings.Split(s, ".")
	for i := 0; i < len(labels); i++ {
		tail := strings.Join(labels[i:], ".")
		for _, c := range countries {
			for _, suf := range c.Suffixes {
				if strings.EqualFold(strings.TrimPrefix(suf, "."), tail) {
					return c.Code
				}
			}
		}
	}
	return ""
}
