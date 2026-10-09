package domain

import (
	"strings"

	"golang.org/x/net/idna"
)

func idnaToASCII(host string) (string, error) {
	return idna.Lookup.ToASCII(host)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// set builds a lookup from a whitespace-separated list. Tables are written this
// way rather than as map literals for two reasons: a duplicated entry is a
// silent no-op in a string list but a compile error in a map literal, and the
// list form keeps hundreds of ccTLD entries readable.
func set(list string) map[string]struct{} {
	out := make(map[string]struct{}, 64)
	for _, f := range strings.Fields(list) {
		out[strings.ToLower(f)] = struct{}{}
	}
	return out
}

// multiPartSuffixes are public suffixes with more than one label. Getting these
// wrong is expensive in both directions: treat "bbc.co.uk" as suffix "co.uk"
// and the BBC merges with every other UK site into one giant candidate; miss
// "com.au" and "example.com.au" collapses into "com.au", merging two companies.
var multiPartSuffixes = set(`
co.uk org.uk me.uk ltd.uk plc.uk net.uk sch.uk ac.uk gov.uk nhs.uk police.uk mod.uk
co.ie org.ie
com.au net.au org.au edu.au gov.au id.au asn.au
co.nz net.nz org.nz govt.nz ac.nz school.nz geek.nz kiwi.nz maori.nz iwi.nz health.nz
com.br net.br org.br gov.br edu.br
com.ar net.ar org.ar gob.ar edu.ar
com.mx org.mx gob.mx edu.mx
com.co net.co org.co edu.co gov.co
com.pe com.ve com.uy com.ec com.bo com.py com.do com.gt com.sv com.hn co.cr com.ni com.pa
co.at or.at ac.at gv.at priv.at
co.fr nom.fr prd.fr tm.fr asso.fr gouv.fr
com.es org.es nom.es gob.es edu.es
com.pt org.pt nome.pt gov.pt edu.pt
co.it gov.it edu.it
co.nl org.nl net.nl gov.nl edu.nl
co.se org.se nu.se or.se
co.no priv.no
co.dk org.dk
com.pl net.pl org.pl gov.pl edu.pl
com.ua net.ua org.ua gov.ua edu.ua
co.hu org.hu gov.hu
com.ro org.ro gov.ro
com.gr org.gr gov.gr edu.gr
com.tr net.tr org.tr gov.tr edu.tr
com.ru net.ru org.ru
com.bg co.rs co.ba com.hr com.si
com.cy com.mt com.ee com.lv com.lt
co.is com.kw com.qa com.bh com.om
co.ke or.ke co.ug com.tz
co.in net.in org.in gen.in firm.in ind.in gov.in ac.in edu.in res.in
co.jp ne.jp or.jp ac.jp go.jp lg.jp ed.jp gr.jp
co.kr ne.kr or.kr re.kr pe.kr go.kr
com.cn net.cn org.cn gov.cn edu.cn ac.cn
com.hk org.hk edu.hk gov.hk
com.tw org.tw edu.tw gov.tw
com.sg org.sg edu.sg gov.sg
com.my net.my org.my gov.my edu.my
co.id or.id ac.id go.id web.id
co.th in.th ac.th go.th
co.ph com.ph net.ph org.ph
com.vn net.vn org.vn gov.vn edu.vn
co.il org.il net.il ac.il gov.il
com.sa net.sa org.sa gov.sa edu.sa
com.eg net.eg org.eg gov.eg edu.eg
com.ng org.ng gov.ng edu.ng
com.pk net.pk org.pk gov.pk edu.pk
com.bd org.bd gov.bd
com.lk com.np com.gh com.et com.mz co.zw
com.fj com.pg
`)

// threePartSuffixes are public suffixes with three labels. The common pattern
// is a second-level domain type under a country suffix, and treating the third
// label as a company name merges unrelated organisations: without "sch.uk" the
// University of Leeds reduces to "sch.uk" and merges with every other UK school.
var threePartSuffixes = set(`
sch.uk ac.uk gov.uk nhs.uk police.uk mod.uk
edu.au gov.au net.au org.au
co.nz govt.nz ac.nz school.nz geek.nz kiwi.nz maori.nz iwi.nz health.nz
co.in net.in org.in gen.in firm.in ind.in gov.in ac.in edu.in
co.jp ne.jp or.jp ac.jp go.jp lg.jp ed.jp gr.jp
co.kr ne.kr or.kr go.kr re.kr pe.kr
com.cn net.cn org.cn gov.cn edu.cn ac.cn
com.hk org.hk edu.hk gov.hk
com.sg org.sg edu.sg gov.sg
com.tw org.tw edu.tw gov.tw
co.id or.id ac.id go.id web.id
co.th in.th ac.th go.th
co.il org.il net.il ac.il gov.il
`)

// genericSuffixes are suffixes that carry no country meaning: the classic
// open TLDs and the new gTLDs. A .com domain says nothing about where a company
// is, and treating it as US would put a false geographic claim on every
// candidate.
//
// Country-code TLDs are deliberately absent. They look like two-letter gTLDs,
// which is exactly why this table is the dangerous one to get right: including
// "de" here would make every German company look like it has no location at
// all. Country suffixes are resolved through the country registry, which is the
// single authority on that question. A ccTLD with no registry entry resolves to
// no country, which is the honest answer rather than a guess.
var genericSuffixes = set(`
com org net int edu gov mil info biz name pro xxx
app dev io ai sh go so to me tv cc xyz online site tech cloud digital
systems solutions services software network agency studio design media
group company works world today life space website page email chat store
shop blog news wiki help support docs live click run host press wiki
`)
