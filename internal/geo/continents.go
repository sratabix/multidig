package geo

import "strings"

const (
	Africa       = "AF"
	Antarctica   = "AN"
	Asia         = "AS"
	Europe       = "EU"
	NorthAmerica = "NA"
	Oceania      = "OC"
	SouthAmerica = "SA"
)

var Order = []string{Europe, NorthAmerica, SouthAmerica, Asia, Africa, Oceania, Antarctica}

var Default = []string{Europe, NorthAmerica, SouthAmerica, Asia, Africa, Oceania}

var names = map[string]string{
	Africa:       "Africa",
	Antarctica:   "Antarctica",
	Asia:         "Asia",
	Europe:       "Europe",
	NorthAmerica: "North America",
	Oceania:      "Oceania",
	SouthAmerica: "South America",
}

var groups = map[string]string{
	Africa:       "AO BF BI BJ BW CD CF CG CI CM CV DJ DZ EG EH ER ET GA GH GM GN GQ GW KE KM LR LS LY MA MG ML MR MU MW MZ NA NE NG RE RW SC SD SH SL SN SO SS ST SZ TD TG TN TZ UG YT ZA ZM ZW",
	Antarctica:   "AQ BV GS HM TF",
	Asia:         "AE AF AM AZ BD BH BN BT CC CN CX CY GE HK ID IL IN IO IQ IR JO JP KG KH KP KR KW KZ LA LB LK MM MN MO MV MY NP OM PH PK PS QA SA SG SY TH TJ TM TL TR TW UZ VN YE",
	Europe:       "AD AL AT AX BA BE BG BY CH CZ DE DK EE ES FI FO FR GB GG GI GR HR HU IE IM IS IT JE LI LT LU LV MC MD ME MK MT NL NO PL PT RO RS RU SE SI SJ SK SM UA VA XK",
	NorthAmerica: "AG AI AW BB BL BM BQ BS BZ CA CR CU CW DM DO GD GL GP GT HN HT JM KN KY LC MF MQ MS MX NI PA PM PR SV SX TC TT US VC VG VI",
	Oceania:      "AS AU CK FJ FM GU KI MH MP NC NF NR NU NZ PF PG PN PW SB TK TO TV UM VU WF WS",
	SouthAmerica: "AR BO BR CL CO EC FK GF GY PE PY SR UY VE",
}

var countryToContinent = func() map[string]string {
	m := make(map[string]string, 256)
	for continent, list := range groups {
		for _, cc := range strings.Fields(list) {
			m[cc] = continent
		}
	}
	return m
}()

func Continent(countryCode string) (string, bool) {
	c, ok := countryToContinent[strings.ToUpper(strings.TrimSpace(countryCode))]
	return c, ok
}

func Name(continent string) string {
	if n, ok := names[strings.ToUpper(continent)]; ok {
		return n
	}
	return continent
}

func Valid(continent string) bool {
	_, ok := names[strings.ToUpper(continent)]
	return ok
}

func SortKey(continent string) int {
	for i, c := range Order {
		if c == continent {
			return i
		}
	}
	return len(Order)
}
