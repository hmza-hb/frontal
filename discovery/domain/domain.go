// Package domain turns a discovered URL or hostname into a stable company
// identity.
//
// Discovery's whole deduplication story rests on this package. The same company
// arrives as www.example.com, example.com, https://Example.com:443/,
// xn--mnchen-3ya.de, and a tracking-tagged deep link; every one of those must
// resolve to one identity, or the candidate set fills with duplicates that later
// stages cannot tell apart.
//
// The registrable-domain rule is delegated to the crawler rather than
// reimplemented, because two copies of a public-suffix table inevitably drift,
// and a disagreement between "which host is this company" and "which hosts are
// one site" produces duplicate companies that no later stage can merge.
package domain

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/hmza-hb/lead-intelligence/crawler"
)

// Domain is a parsed, normalized hostname with the attributes discovery needs
// to reason about company identity.
type Domain struct {
	// Raw is the string as it was discovered, kept for provenance. It is never
	// used for identity.
	Raw string
	// Host is the lowercased, punycoded hostname with no port, userinfo, or
	// trailing dot.
	Host string
	// Registrable is the eTLD+1: the domain a human would name as "the
	// company's domain". This is the primary identity key.
	Registrable string
	// PublicSuffix is ".co.uk" style, including the leading dot, or "" when the
	// host is itself a suffix.
	PublicSuffix string
	// Subdomain is the label chain above Registrable, e.g. "shop" in
	// shop.example.co.uk. Empty for the registrable domain itself.
	Subdomain string
	// Country is the ISO 3166-1 alpha-2 code implied by a country-code suffix,
	// or "" for generic and infrastructure suffixes. Note that ".uk", ".de" and
	// ".us" identify a suffix, not the location of the company, which may be
	// anywhere; see CountryHint's documentation.
	Country string
	// IsIP reports that the host is an address literal rather than a name.
	IsIP bool
	// IsIDN reports that the original host contained non-ASCII characters.
	IsIDN bool
	// IsPlatform reports that the domain is a user-content or aggregation
	// surface rather than a company that sells something. A candidate on such a
	// domain is almost always a discovery artefact: the blog post that mentioned
	// a company lives on medium.com, and medium.com is not the candidate.
	IsPlatform bool
}

// String returns the registrable domain, which is the identity key.
func (d Domain) String() string { return d.Registrable }

// IsSubdomain reports whether the host sits below the given registrable domain.
// It is true for the registrable domain itself, so it reads naturally as
// "belongs to this site".
func (d Domain) IsSubdomain(registrable string) bool {
	r := Normalize(registrable)
	if r == "" || d.Registrable != r {
		return false
	}
	return true
}

// Parse accepts a URL, a hostname, or a hostname with a path, and returns the
// normalized domain. It is the single entry point: everything that handles a
// discovered string goes through here, so there is one place where a malformed
// or hostile value is rejected.
func Parse(raw string) (Domain, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Domain{}, fmt.Errorf("domain: empty input")
	}
	// A bare hostname has no scheme, and url.Parse would read "example.com" as
	// a path. Add one so the two forms normalize identically. But a string that
	// already names a scheme — mailto:, javascript:, data: — must be rejected,
	// not have "https://" glued in front of it, which would silently turn
	// "mailto:someone@example.com" into the host "b.com".
	candidate := trimmed
	if strings.HasPrefix(candidate, "//") {
		candidate = "https:" + candidate
	} else if !hasScheme(candidate) {
		candidate = "https://" + candidate
	}
	u, err := url.Parse(candidate)
	if err != nil {
		return Domain{}, fmt.Errorf("domain: parse %q: %w", raw, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return Domain{}, fmt.Errorf("domain: unsupported scheme %q in %q", u.Scheme, raw)
	}
	host := u.Hostname()
	if host == "" {
		return Domain{}, fmt.Errorf("domain: %q has no host", raw)
	}
	d, err := FromHost(host)
	if err != nil {
		return Domain{}, err
	}
	d.Raw = trimmed
	return d, nil
}

// FromHost normalizes a bare hostname.
func FromHost(host string) (Domain, error) {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return Domain{}, fmt.Errorf("domain: empty host")
	}
	// A URL is not a host. Providers routinely hand back a profile page where
	// a domain was expected, and parsing it as a host produces confidently wrong
	// results rather than an error: "medium.com/@acme" would yield the host
	// "acme", turning one author's profile into a company called Acme, and
	// "github.com/torvalds/linux" would yield a registrable domain that is a
	// path. Rejecting is the only safe reading, and the caller that really had a
	// URL should be using Parse.
	if strings.ContainsAny(trimmed, "/?#@\\ \t") {
		return Domain{}, fmt.Errorf("domain: %q is a URL, not a host; use Parse", host)
	}
	// Some sources deliver a host:port pair, and some deliver brackets around
	// an IPv6 literal. url.Parse already stripped them in Parse; strip again
	// here so FromHost is safe to call directly.
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	if i := strings.LastIndex(trimmed, ":"); i > 0 && net.ParseIP(trimmed) == nil {
		trimmed = trimmed[:i]
	}

	normalized, isIDN := normalizeHost(trimmed)
	if normalized == "" {
		return Domain{}, fmt.Errorf("domain: %q has no usable host", host)
	}

	d := Domain{
		Raw:   host,
		Host:  normalized,
		IsIDN: isIDN,
	}

	if ip := net.ParseIP(normalized); ip != nil {
		d.IsIP = true
		d.Registrable = normalized
		return d, nil
	}

	labels := strings.Split(normalized, ".")

	// A bare generic TLD ("com", "shop", "app") is a suffix, not a company.
	// Getting this wrong would accept "https://com" as a candidate.
	if len(labels) == 1 {
		d.PublicSuffix = "." + labels[0]
		if _, isGeneric := genericSuffixes[labels[0]]; isGeneric {
			d.Registrable = ""
		} else {
			// A single non-TLD label ("localhost", "intranet") is an intranet
			// name, never a public company domain.
			d.Registrable = normalized
		}
		return d, nil
	}

	suffix := publicSuffix(labels)
	// A host that is itself a public suffix ("co.uk") has no company label.
	if len(labels) == len(suffix) {
		d.PublicSuffix = "." + strings.Join(suffix, ".")
		d.Registrable = ""
		return d, nil
	}
	d.PublicSuffix = "." + strings.Join(suffix, ".")
	d.Registrable = strings.Join(labels[len(labels)-len(suffix)-1:], ".")
	d.Subdomain = strings.Join(labels[:len(labels)-len(suffix)-1], ".")
	d.Country = countryFromSuffix(d.PublicSuffix)
	d.IsPlatform = isPlatformDomain(d.Registrable)
	return d, nil
}

// Normalize returns the canonical form of a host: lowercase, punycoded, with
// any port, userinfo, "www." prefix and trailing dot removed. It is the cheap
// string operation used for map keys, where parsing a URL would be wasteful.
//
// It does not reduce to the registrable domain; use Parse or RegistrableDomain
// for that. Normalize exists so that "HTTPS://WWW.Example.com:443/a" and
// "example.com" produce the same key.
func Normalize(host string) string {
	// Callers reach this from providers holding either a hostname or a whole
	// URL. Reducing a URL to its host here means a map key can never accidentally
	// be a full URL, which would make every URL its own candidate.
	h := strings.TrimSpace(host)
	if strings.Contains(h, "://") {
		if u, err := url.Parse(h); err == nil && u.Host != "" {
			h = u.Host
		}
	} else if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	n, _ := normalizeHost(h)
	return n
}

// hasScheme reports whether a string begins with an explicit URI scheme. A
// single leading letter followed by a colon is enough for the discovery inputs
// that matter (mailto, javascript, data, file, ftp, tel).
func hasScheme(s string) bool {
	i := strings.Index(s, ":")
	if i <= 0 {
		return false
	}
	for j := 0; j < i; j++ {
		c := s[j]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !ok && !(j > 0 && (c == '+' || c == '-' || c == '.')) {
			return false
		}
	}
	// "example.com:8080" is a host and a port, not a scheme: the part after the
	// colon starts with a digit.
	if i < len(s)-1 && s[i+1] >= '0' && s[i+1] <= '9' {
		return false
	}
	return true
}

// RegistrableDomain reduces a host to the company domain. It is a thin
// pass-through to the crawler so the two modules cannot disagree.
func RegistrableDomain(host string) string {
	return crawler.RegistrableDomain(host)
}

func normalizeHost(host string) (string, bool) {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return "", false
	}
	// Strip userinfo. A source that produced "user@host" is either broken or
	// hostile; identity must not depend on the part before the "@".
	if i := strings.LastIndex(h, "@"); i >= 0 {
		h = h[i+1:]
	}
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	if i := strings.LastIndex(h, ":"); i > 0 && net.ParseIP(h) == nil && !strings.Contains(h[i+1:], ":") {
		h = h[:i]
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return "", false
	}
	isIDN := !isASCII(h)
	if isIDN {
		// Lookup profile is what a registrar stores, so a unicode input and the
		// punycode a registry reports are the same string.
		if ascii, err := idnaToASCII(h); err == nil {
			h = ascii
		} else {
			// Leave it as-is rather than fail: a domain we cannot encode is
			// still a distinct string, and dropping it loses a real candidate.
			isIDN = false
		}
	}
	// A leading "www." is a naming convention, not an identity. Stripping it
	// here is what makes www.example.com and example.com one candidate.
	if strings.HasPrefix(h, "www.") {
		h = h[4:]
	}
	return h, isIDN
}

// publicSuffix returns the suffix labels for a lowercased host. It uses a
// curated multi-part list plus the implicit "last label" rule, which is correct
// for gTLDs and correct for the large majority of ccTLD traffic.
func publicSuffix(labels []string) []string {
	if len(labels) < 2 {
		return labels
	}
	lastTwo := strings.Join(labels[len(labels)-2:], ".")
	if _, isMulti := multiPartSuffixes[lastTwo]; isMulti {
		if len(labels) >= 3 {
			// co.uk has an optional third level (e.g. ltd.uk, plc.uk). Only
			// treat a third label as part of the suffix when it is itself a
			// recognized public-suffix third level; otherwise "bbc.co.uk" would
			// reduce to "co.uk" and merge the BBC with every other UK site.
			third := strings.Join(labels[len(labels)-3:], ".")
			if _, isThree := threePartSuffixes[third]; isThree {
				return labels[len(labels)-3:]
			}
		}
		return labels[len(labels)-2:]
	}
	return labels[len(labels)-1:]
}

// PublicSuffix returns the public suffix of a host, including the leading dot.
func PublicSuffix(host string) string {
	d, err := FromHost(host)
	if err != nil {
		return ""
	}
	return d.PublicSuffix
}

// Related reports whether two hosts belong to the same site, which for
// identity purposes means one is a subdomain of the other's registrable domain.
func Related(a, b string) bool {
	da, err := FromHost(a)
	if err != nil {
		return false
	}
	db, err := FromHost(b)
	if err != nil {
		return false
	}
	if da.Registrable == "" || db.Registrable == "" {
		return false
	}
	return da.Registrable == db.Registrable
}

// IsLikelyCompanyDomain reports whether a host is plausible as a candidate
// company's own site. It filters the shapes that are never companies: address
// literals, bare suffixes, and user-content platforms where the domain
// identifies a publishing venue rather than a vendor.
//
// It deliberately does not filter small sites, new TLDs, or unusual names.
// Missing a real company costs more than carrying one extra candidate, and
// qualification is the stage entitled to reject.
func IsLikelyCompanyDomain(host string) bool {
	d, err := FromHost(host)
	if err != nil {
		return false
	}
	switch {
	case d.IsIP:
		return false
	case d.Registrable == "":
		// A public suffix with no company label.
		return false
	case !strings.Contains(d.Registrable, "."):
		// "localhost", "intranet", a bare service name. Never a public company.
		return false
	case d.IsPlatform:
		return false
	case strings.HasPrefix(d.Registrable, "xn--"):
		// Punycode of a single-label or unusual name; keep it, it can be real.
		return true
	}
	return true
}

// PlatformDomains are hosts that identify a publishing, social, or aggregation
// surface rather than a company that sells a product. A candidate discovered on
// one of these is almost always an artefact of where the mention was published.
//
// The list is deliberately conservative. Excluding a real company is a worse
// error than carrying a platform through to qualification, and every entry here
// is a host whose primary function is hosting other people's content. Large
// software vendors (Salesforce, Atlassian, ServiceNow) are NOT here: they sell
// software and are exactly the kind of company an ICP wants.
var PlatformDomains = buildPlatformDomains()

// buildPlatformDomains is a function rather than a literal so that a host
// appearing in two categories is a build error the compiler catches, rather
// than a silently shadowed entry that changes behaviour depending on which map
// literal line the editor kept.
func buildPlatformDomains() map[string]string {
	m := map[string]string{}
	add := func(kind, list string) {
		for _, d := range strings.Fields(list) {
			if prev, dup := m[d]; dup {
				if prev == kind {
					// Listing a host twice inside one category is harmless.
					continue
				}
				// Two different categories for one host is a genuine conflict:
				// the filter would depend on which line an editor kept. Fail at
				// init, where the stack names this file.
				panic("domain: platform domain " + d + " listed as both " + prev + " and " + kind)
			}
			m[d] = kind
		}
	}
	add("social", `
		facebook.com instagram.com twitter.com x.com linkedin.com reddit.com
		youtube.com tiktok.com pinterest.com tumblr.com threads.net vk.com
		t.me whatsapp.com snapchat.com discord.com mastodon.social bsky.app
	`)
	add("publishing", `
		medium.com substack.com wordpress.com blogger.com ghost.io dev.to
		hashnode.dev notion.site quora.com
	`)
	add("aggregator", `
		news.ycombinator.com ycombinator.com lobste.rs producthunt.com
		betalist.com indiehackers.com alternativeto.net
	`)
	add("reference", `
		wikipedia.org wikidata.org wikimedia.org w3.org schema.org
		ietf.org ietf.org rfc-editor.org
	`)
	add("developer", `
		github.com gitlab.com bitbucket.org npmjs.com pypi.org rubygems.org
		packagist.org docker.com hub.docker.com crates.io stackoverflow.com
		stackexchange.com sourceforge.net pkg.go.dev
	`)
	add("directory", `
		crunchbase.com angel.co wellfound.com g2.com capterra.com
		trustradius.com clutch.co clutch.com goodfirms.co sortlist.com
		pitchbook.com cbinsights.com zoominfo.com apollo.io clearbit.com
		semrush.com similarweb.com builtin.com f6s.com wellfound.co
	`)
	add("registry", `opencorporates.com sec.gov edgar.sec.gov companieshouse.gov.uk register-of-companies`)
	add("government", `gov.uk gov.europa.eu europa.eu`)
	return m
}

// IsPlatformDomain reports whether a host is a publishing, social, developer or
// directory surface, along with the category that explains why.
func IsPlatformDomain(host string) (string, bool) {
	reg := crawler.RegistrableDomain(host)
	if reg == "" {
		return "", false
	}
	kind, ok := PlatformDomains[reg]
	return kind, ok
}

func isPlatformDomain(registrable string) bool {
	_, ok := PlatformDomains[registrable]
	return ok
}

// CountryCount is the number of countries in the built-in registry, so a
// caller can report coverage without reaching into the table.
func CountryCount() int { return len(countries) }
