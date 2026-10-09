package domain

import (
	"testing"
)

func TestParseCanonicalizesHost(t *testing.T) {
	cases := []struct {
		raw          string
		registrable  string
		publicSuffix string
		subdomain    string
	}{
		{"https://example.com", "example.com", ".com", ""},
		{"https://www.example.com", "example.com", ".com", ""},
		{"http://Example.COM/Path?x=1", "example.com", ".com", ""},
		{"https://example.com:8443/x", "example.com", ".com", ""},
		{"https://app.example.com", "example.com", ".com", "app"},
		{"https://a.b.example.com", "example.com", ".com", "a.b"},
		{"example.com", "example.com", ".com", ""},
		{"  https://www.example.com/  ", "example.com", ".com", ""},
		{"https://shop.example.co.uk", "example.co.uk", ".co.uk", "shop"},
		{"https://www.example.com.au", "example.com.au", ".com.au", ""},
		{"https://a.example.com.au", "example.com.au", ".com.au", "a"},
	}
	for _, tc := range cases {
		d, err := Parse(tc.raw)
		if err != nil {
			t.Errorf("Parse(%q) = %v", tc.raw, err)
			continue
		}
		if d.Registrable != tc.registrable {
			t.Errorf("Parse(%q).Registrable = %q, want %q", tc.raw, d.Registrable, tc.registrable)
		}
		if d.PublicSuffix != tc.publicSuffix {
			t.Errorf("Parse(%q).PublicSuffix = %q, want %q", tc.raw, d.PublicSuffix, tc.publicSuffix)
		}
		if d.Subdomain != tc.subdomain {
			t.Errorf("Parse(%q).Subdomain = %q, want %q", tc.raw, d.Subdomain, tc.subdomain)
		}
	}
}

func TestParseRejectsNonWebAndEmpty(t *testing.T) {
	// A discovered string arrives from a search result, a sitemap, or a
	// certificate log. All of them can contain anything.
	for _, raw := range []string{
		"", "   ", "://", "https://", "http://",
		"file:///etc/passwd", "ftp://example.com", "gopher://x",
		"javascript:alert(1)", "data:text/html,x", "mailto:a@b.com",
		"not a url at all", "http://:80", "https://exa mple.com",
	} {
		if d, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) = %+v, want an error", raw, d)
		}
	}
}

func TestParseRejectsPublicSuffixAsCompany(t *testing.T) {
	// A host that is only a suffix has no company label, so it cannot be a
	// candidate identity.
	for _, raw := range []string{"co.uk", "com.au", "https://com", "https://localhost"} {
		d, err := Parse(raw)
		if err != nil {
			// Accepting an error is fine too; what must not happen is a
			// non-empty Registrable for a suffix-only host.
			continue
		}
		if d.Registrable == "" {
			continue
		}
		if raw == "https://localhost" {
			continue // single label, kept as-is; filtered by IsLikelyCompanyDomain
		}
		if d.Registrable == "co.uk" || d.Registrable == "com.au" || d.Registrable == "com" {
			t.Errorf("Parse(%q).Registrable = %q, want empty for a suffix-only host", raw, d.Registrable)
		}
	}
}

func TestRegistrableDomainKeepsUnrelatedCompaniesApart(t *testing.T) {
	// The most expensive error in this package is merging two real companies
	// into one candidate. These are the cases that catch it.
	distinct := [][2]string{
		{"bbc.co.uk", "independent.co.uk"},
		{"example.com.au", "other.com.au"},
		{"example.co.jp", "other.co.jp"},
		{"example.com.br", "other.com.br"},
		{"example.co.uk", "example.com"},
		{"example.ie", "example.co.uk"},
		// Different second-level types on one ccTLD are different companies.
		{"shop.example.com", "example.com"}, // subdomain folds up: same company
		{"example.org", "example.net"},
		// A three-label public suffix must not be treated as a company name.
		{"a.sch.uk", "b.sch.uk"},
		{"x.ac.uk", "y.ac.uk"},
	}
	for _, p := range distinct {
		a, err := FromHost(p[0])
		if err != nil {
			t.Fatalf("FromHost(%q): %v", p[0], err)
		}
		b, err := FromHost(p[1])
		if err != nil {
			t.Fatalf("FromHost(%q): %v", p[1], err)
		}
		if a.Registrable == b.Registrable && p[0] != "shop.example.com" {
			t.Errorf("%q and %q both resolved to %q; two companies merged",
				p[0], p[1], a.Registrable)
		}
	}
}

func TestSubdomainFoldsIntoOneCompany(t *testing.T) {
	// The mirror of the test above: the same company seen through five
	// hostnames must be one identity.
	hosts := []string{
		"example.com", "www.example.com", "app.example.com", "blog.example.com",
		"shop.example.com", "api.example.com", "EU-WEST-1.example.com",
	}
	want := "example.com"
	for _, h := range hosts {
		d, err := FromHost(h)
		if err != nil {
			t.Fatalf("FromHost(%q): %v", h, err)
		}
		if d.Registrable != want {
			t.Errorf("FromHost(%q).Registrable = %q, want %q", h, d.Registrable, want)
		}
	}
}

func TestCountryFromSuffix(t *testing.T) {
	cases := []struct{ host, want string }{
		{"example.de", "DE"},
		{"example.co.uk", "GB"},
		{"example.com.au", "AU"},
		{"example.co.jp", "JP"},
		{"example.com.br", "BR"},
		{"example.co.in", "IN"},
		{"example.com.sg", "SG"},
		{"example.co.za", "ZA"},
		{"example.com", ""},  // .com says nothing about location
		{"example.io", ""},   // new gTLD
		{"example.ai", ""},   // new gTLD
		{"example.dev", ""},  // new gTLD
		{"example.app", ""},  // new gTLD
		{"example.xyz", ""},  // new gTLD
		{"example.tech", ""}, // new gTLD
		{"sub.example.co.uk", "GB"},
	}
	for _, tc := range cases {
		d, err := FromHost(tc.host)
		if err != nil {
			t.Fatalf("FromHost(%q): %v", tc.host, err)
		}
		if d.Country != tc.want {
			t.Errorf("FromHost(%q).Country = %q, want %q", tc.host, d.Country, tc.want)
		}
	}
}

func TestIDNAndPunycodeAreOneDomain(t *testing.T) {
	// A unicode domain and its punycode form are one host. Splitting them
	// creates a duplicate candidate that no later stage can merge.
	unicode := "münchen.de"
	puny := "xn--mnchen-3ya.de"
	du, err := FromHost(unicode)
	if err != nil {
		t.Fatalf("FromHost(%q): %v", unicode, err)
	}
	dp, err := FromHost(puny)
	if err != nil {
		t.Fatalf("FromHost(%q): %v", puny, err)
	}
	if du.Registrable != dp.Registrable {
		t.Errorf("unicode %q and punycode %q resolved to %q and %q",
			unicode, puny, du.Registrable, dp.Registrable)
	}
	if !du.IsIDN {
		t.Error("a unicode input must be flagged IsIDN")
	}
	if dp.IsIDN {
		t.Error("an already-ASCII input must not be flagged IsIDN")
	}
	// And the same through the URL entry point.
	d3, err := Parse("https://www.münchen.de/path")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d3.Registrable != dp.Registrable {
		t.Errorf("Parse(unicode URL) = %q, want %q", d3.Registrable, dp.Registrable)
	}
}

func TestIsLikelyCompanyDomainRejectsNonCompanies(t *testing.T) {
	notCompanies := []string{
		"github.com", "medium.com", "linkedin.com", "crunchbase.com",
		"twitter.com", "x.com", "wikipedia.org", "reddit.com",
		"news.ycombinator.com", "npmjs.com", "producthunt.com",
		"stackoverflow.com", "dev.to", "quora.com", "substack.com",
		"127.0.0.1", "10.0.0.1", "::1", "localhost",
	}
	for _, h := range notCompanies {
		if IsLikelyCompanyDomain(h) {
			t.Errorf("IsLikelyCompanyDomain(%q) = true, want false", h)
		}
	}
	companies := []string{
		"example.com", "acme.io", "shopify.com", "salesforce.com",
		"atlassian.com", "servicenow.com", "smallstartup.co",
		// A company whose product is a platform is still a company.
		"stripe.com", "datadog.com", "mongodb.com", "twilio.com",
		// Unusual but real TLDs and shapes must not be filtered.
		"company.ai", "thing.dev", "brand.app", "x.io", "a-b-c.com",
		"xn--80ak6aa92e.com", "123numeric.com",
	}
	for _, h := range companies {
		if !IsLikelyCompanyDomain(h) {
			t.Errorf("IsLikelyCompanyDomain(%q) = false, want true", h)
		}
	}
}

func TestRelated(t *testing.T) {
	same := [][2]string{
		{"example.com", "www.example.com"},
		{"app.example.com", "example.com"},
		{"a.b.example.co.uk", "example.co.uk"},
		{"example.com", "example.com"},
	}
	for _, p := range same {
		if !Related(p[0], p[1]) {
			t.Errorf("Related(%q, %q) = false, want true", p[0], p[1])
		}
	}
	diff := [][2]string{
		{"example.com", "example.org"},
		{"example.com", "notexample.com"},
		{"a.co.uk", "b.co.uk"},
		{"example.com", "example.com.au"},
		{"example.com", ""},
		{"", "example.com"},
		{"not a domain", "example.com"},
	}
	for _, p := range diff {
		if Related(p[0], p[1]) {
			t.Errorf("Related(%q, %q) = true, want false", p[0], p[1])
		}
	}
}

func TestNormalizeStripsDecoration(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"HTTPS://WWW.Example.COM:443/path?q=1#f", "example.com"},
		{"  www.example.com.  ", "example.com"},
		{"user@example.com", "example.com"},
		{"APP.example.com", "app.example.com"},
		{"[::1]", "::1"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := Normalize(tc.raw); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestFromHostHandlesAddresses(t *testing.T) {
	for _, h := range []string{"127.0.0.1", "192.168.1.1", "8.8.8.8", "::1", "[2001:db8::1]"} {
		d, err := FromHost(h)
		if err != nil {
			t.Fatalf("FromHost(%q): %v", h, err)
		}
		if !d.IsIP {
			t.Errorf("FromHost(%q).IsIP = false, want true", h)
		}
		// An address is never a company domain.
		if IsLikelyCompanyDomain(h) {
			t.Errorf("IsLikelyCompanyDomain(%q) = true, want false for an address", h)
		}
	}
}

func TestCountryRegistryLookups(t *testing.T) {
	if _, ok := LookupCountry("de"); !ok {
		t.Error("LookupCountry(de) failed; matching must be case-insensitive")
	}
	if _, ok := LookupCountry("  GB  "); !ok {
		t.Error("LookupCountry must tolerate whitespace")
	}
	if _, ok := LookupCountry("ZZ"); ok {
		t.Error("LookupCountry(ZZ) succeeded; unknown codes must fail")
	}
	byName := []struct {
		name string
		want string
	}{
		{"Germany", "DE"},
		{"UK", "GB"},
		{"United States", "US"},
		{"United States of America", ""}, // not an alias we claim to know
		{"Netherlands", "NL"},
		{"United Arab Emirates", "AE"},
		{"Türkiye", "TR"},
	}
	for _, tc := range byName {
		c, ok := LookupCountryByName(tc.name)
		if tc.want == "" {
			if ok {
				t.Errorf("LookupCountryByName(%q) = %+v, want not found", tc.name, c)
			}
			continue
		}
		if !ok {
			t.Errorf("LookupCountryByName(%q) not found", tc.name)
			continue
		}
		if c.Code != tc.want {
			t.Errorf("LookupCountryByName(%q) = %s, want %s", tc.name, c.Code, tc.want)
		}
	}
}

func TestCountryRegistryIsInternallyConsistent(t *testing.T) {
	// A country whose suffix is also listed as generic is a contradiction: one
	// of the two lookups would always be wrong, and which one wins would depend
	// on call order.
	for _, c := range Countries() {
		if c.Code == "" || c.Name == "" {
			t.Errorf("country %+v is missing a code or name", c)
		}
		if len(c.Code) != 2 {
			t.Errorf("country %q has a non-alpha-2 code", c.Code)
		}
		for _, suf := range c.Suffixes {
			s := trimDot(suf)
			if _, generic := genericSuffixes[s]; generic {
				t.Errorf("country %s lists %q as a suffix but it is also marked generic", c.Code, s)
			}
			d, err := FromHost("example." + s)
			if err != nil {
				t.Errorf("FromHost(example%s): %v", s, err)
				continue
			}
			if d.Country != c.Code {
				t.Errorf("suffix %q maps to %s, want %s", s, d.Country, c.Code)
			}
		}
		if len(c.Cities) == 0 {
			t.Errorf("country %s has no cities; geographic queries would be impossible", c.Code)
		}
	}
}

func trimDot(s string) string {
	if len(s) > 0 && s[0] == '.' {
		return s[1:]
	}
	return s
}

func TestCountryHasLocalVocabulary(t *testing.T) {
	// Multilingual discovery depends on the registry carrying real terms for
	// the languages an ICP can name. An entry with no English term is useless.
	for _, c := range Countries() {
		if c.Term("en") == "" {
			t.Errorf("country %s has no English term", c.Code)
		}
	}
	// A language the registry does not carry must return empty rather than the
	// English term, so the caller can skip the variant instead of issuing an
	// English query labelled as localized.
	de, ok := LookupCountry("DE")
	if !ok {
		t.Fatal("DE missing")
	}
	if got := de.Term("sw"); got != "" {
		t.Errorf("de.term(sw) = %q, want empty for an unlisted language", got)
	}
	if got := de.Term("de"); got == "" || got == de.Term("en") {
		t.Errorf("de.term(de) = %q, want a German term distinct from English", got)
	}
}

func TestCountryCountIsStable(t *testing.T) {
	// The registry is data; a count catches an entry accidentally dropped by a
	// bad edit, which is otherwise invisible.
	if n := CountryCount(); n < 30 {
		t.Errorf("CountryCount() = %d, want the full registry (30+)", n)
	}
}

func TestFromHostRejectsURLs(t *testing.T) {
	// Providers routinely return a profile page where a domain was expected.
	// Parsing that as a host produced confidently wrong answers: "medium.com/@acme"
	// became the host "acme", turning one author's profile into a company
	// called Acme, and a GitHub path became a "registrable domain" that is a
	// path. An error is the only safe reading.
	for _, in := range []string{
		"medium.com/@acme",
		"github.com/torvalds/linux",
		"linkedin.com/in/ada",
		"https://acme.com/about",
		"acme.com/about?ref=1",
		"acme.com/#team",
		"acme.com\\path",
		"acme.com and globex.com",
	} {
		if d, err := FromHost(in); err == nil {
			t.Errorf("FromHost(%q) = %+v, want an error", in, d)
		}
	}

	// A bare host, an IP, a host:port and an IPv6 literal are all still hosts.
	for _, in := range []string{"acme.com", "www.acme.co.uk", "acme.com:8443", "192.0.2.1", "[2001:db8::1]"} {
		if _, err := FromHost(in); err != nil {
			t.Errorf("FromHost(%q) returned %v, want a parsed host", in, err)
		}
	}
}
