// Package airports resolves airport codes to coordinates.
//
// The table is compiled into the binary by cmd/genairports, so lookups need no
// network access and no data files alongside the executable.
package airports

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Airport is one entry in the table.
type Airport struct {
	IATA    string // 3-letter code, the one printed on a ticket
	ICAO    string // 4-letter code, may be empty
	Name    string
	City    string
	Country string // ISO 3166-1 alpha-2
	Lat     float64
	Lon     float64
}

// Label renders the airport for display, e.g. "LHR - London Heathrow (London, GB)".
func (a Airport) Label() string {
	var b strings.Builder
	b.WriteString(a.IATA)
	if a.Name != "" {
		b.WriteString(" - ")
		b.WriteString(a.Name)
	}
	switch {
	case a.City != "" && a.Country != "":
		b.WriteString(" (" + a.City + ", " + a.Country + ")")
	case a.Country != "":
		b.WriteString(" (" + a.Country + ")")
	}
	return b.String()
}

var (
	once   sync.Once
	byIATA map[string]Airport
	byICAO map[string]Airport
	all    []Airport
)

func load() {
	byIATA = make(map[string]Airport, count)
	byICAO = make(map[string]Airport, count)
	all = make([]Airport, 0, count)

	for _, line := range strings.Split(strings.TrimRight(data, "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			continue
		}
		lat, err1 := strconv.ParseFloat(f[5], 64)
		lon, err2 := strconv.ParseFloat(f[6], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		a := Airport{
			IATA: f[0], ICAO: f[1], Name: f[2], City: f[3], Country: f[4],
			Lat: lat, Lon: lon,
		}
		all = append(all, a)
		if _, dup := byIATA[a.IATA]; !dup {
			byIATA[a.IATA] = a
		}
		if a.ICAO != "" {
			if _, dup := byICAO[a.ICAO]; !dup {
				byICAO[a.ICAO] = a
			}
		}
	}
}

// Count reports how many airports are in the table.
func Count() int {
	once.Do(load)
	return len(all)
}

// Lookup finds an airport by IATA (3-letter) or ICAO (4-letter) code.
func Lookup(code string) (Airport, bool) {
	once.Do(load)
	c := strings.ToUpper(strings.TrimSpace(code))
	switch len(c) {
	case 3:
		a, ok := byIATA[c]
		return a, ok
	case 4:
		a, ok := byICAO[c]
		return a, ok
	}
	return Airport{}, false
}

// Search returns airports whose code, name or city matches the query, best
// matches first, capped at limit. It exists so a mistyped or half-remembered
// code offers something useful instead of a bare failure.
func Search(query string, limit int) []Airport {
	once.Do(load)
	q := strings.ToUpper(strings.TrimSpace(query))
	if q == "" || limit <= 0 {
		return nil
	}

	type scored struct {
		a     Airport
		score int
	}
	var hits []scored
	for _, a := range all {
		switch {
		case a.IATA == q || a.ICAO == q:
			hits = append(hits, scored{a, 0})
		case strings.HasPrefix(strings.ToUpper(a.City), q):
			hits = append(hits, scored{a, 1})
		case strings.HasPrefix(strings.ToUpper(a.Name), q):
			hits = append(hits, scored{a, 2})
		case strings.Contains(strings.ToUpper(a.Name), q):
			hits = append(hits, scored{a, 3})
		case strings.Contains(strings.ToUpper(a.City), q):
			hits = append(hits, scored{a, 4})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score
		}
		return hits[i].a.IATA < hits[j].a.IATA
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Airport, len(hits))
	for i, h := range hits {
		out[i] = h.a
	}
	return out
}
