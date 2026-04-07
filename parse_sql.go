// parse_sql.go — parse men_results.sql and player_profiles.sql into matches.json.gz
//
// Usage: go run parse_sql.go <output.json.gz> <men_results.sql> [women_results.sql] <player_profiles.sql>
//
// Output format:
//   { "matches": [{date, winner, loser}, ...], "players": {"Name": "YYYY-MM-DD", ...} }
//
// Age adjustment: for each 200 days a player is older than their opponent, their effective
// ELO is reduced by 1 when computing expected scores. Birth dates with no profile entry are
// estimated as (first recorded match date) - 20 years.

package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// MatchRecord is a single game result using integer player indices.
type MatchRecord struct {
	Date   string `json:"date"`
	Winner int    `json:"winner"`
	Loser  int    `json:"loser"`
}

// Output is the JSON structure written to disk.
// names[i] is the player name; births[i] is their birth date ("YYYY-MM-DD") or "".
type Output struct {
	Names   []string      `json:"names"`
	Births  []string      `json:"births"`
	Matches []MatchRecord `json:"matches"`
}

// rawMatch is used internally during parsing before player IDs are assigned.
type rawMatch struct {
	Date   string
	Winner string
	Loser  string
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: go run parse_sql.go <output.json.gz> <men_results.sql> [women_results.sql] <player_profiles.sql>")
		os.Exit(1)
	}

	outFile := os.Args[1]
	args := os.Args[2:]

	// Separate results files from profiles file. We detect profiles by filename.
	var resultFiles []string
	var profileFile string
	for _, a := range args {
		if strings.Contains(strings.ToLower(filepath.Base(a)), "profile") {
			profileFile = a
		} else {
			resultFiles = append(resultFiles, a)
		}
	}
	if profileFile == "" {
		log.Fatal("no player_profiles.sql argument found")
	}
	if len(resultFiles) == 0 {
		log.Fatal("no results SQL file provided")
	}

	// --- Parse birth dates ---
	birthDates := parseBirthDates(profileFile)
	fmt.Fprintf(os.Stderr, "profiles: %d players with birth dates\n", len(birthDates))

	// --- Parse matches ---
	seen := map[string]bool{}
	var rawMatches []rawMatch

	for _, rf := range resultFiles {
		newMatches, dupes := parseResults(rf)
		added := 0
		for _, m := range newMatches {
			key := m.Date + "|" + m.Winner + "|" + m.Loser
			if !seen[key] {
				seen[key] = true
				rawMatches = append(rawMatches, m)
				added++
			}
		}
		fmt.Fprintf(os.Stderr, "%s: %d matches parsed, %d duplicates, %d new\n",
			filepath.Base(rf), len(newMatches)+dupes, dupes, added)
	}

	// Sort chronologically.
	sort.Slice(rawMatches, func(i, j int) bool {
		return rawMatches[i].Date < rawMatches[j].Date
	})

	// --- Infer birth dates for players with no profile entry ---
	firstMatch := map[string]string{} // player → earliest match date
	for _, m := range rawMatches {
		for _, name := range []string{m.Winner, m.Loser} {
			if _, hasBirth := birthDates[name]; hasBirth {
				continue
			}
			if existing, ok := firstMatch[name]; !ok || m.Date < existing {
				firstMatch[name] = m.Date
			}
		}
	}
	inferred := 0
	for name, firstDate := range firstMatch {
		t, err := time.Parse("2006-01-02", firstDate)
		if err != nil {
			continue
		}
		assumed := t.AddDate(-20, 0, 0)
		birthDates[name] = assumed.Format("2006-01-02")
		inferred++
	}
	fmt.Fprintf(os.Stderr, "inferred %d birth dates (assumed age 20 at first match)\n", inferred)

	// --- Build player index ---
	// Assign a stable integer ID to each player name in order of first appearance.
	nameIndex := map[string]int{}
	var names []string
	playerID := func(name string) int {
		if id, ok := nameIndex[name]; ok {
			return id
		}
		id := len(names)
		nameIndex[name] = id
		names = append(names, name)
		return id
	}
	// Pre-scan in match order so IDs are assigned chronologically.
	for _, m := range rawMatches {
		playerID(m.Winner)
		playerID(m.Loser)
	}

	// Build birth date array parallel to names.
	births := make([]string, len(names))
	for i, name := range names {
		births[i] = birthDates[name] // "" if not found (shouldn't happen after inference)
	}

	// Build compact match records.
	matchRecords := make([]MatchRecord, len(rawMatches))
	for i, m := range rawMatches {
		matchRecords[i] = MatchRecord{
			Date:   m.Date,
			Winner: nameIndex[m.Winner],
			Loser:  nameIndex[m.Loser],
		}
	}

	// --- Write output ---
	out := Output{
		Names:   names,
		Births:  births,
		Matches: matchRecords,
	}

	f, err := os.Create(outFile)
	if err != nil {
		log.Fatalf("create %s: %v", outFile, err)
	}
	defer f.Close()

	var w interface {
		Write([]byte) (int, error)
		Close() error
	}
	if strings.HasSuffix(outFile, ".gz") {
		gw := gzip.NewWriter(f)
		w = gw
	} else {
		w = &nopCloser{f}
	}

	enc := json.NewEncoder(w)
	if err := enc.Encode(out); err != nil {
		log.Fatalf("encode: %v", err)
	}
	w.Close()

	fi, _ := os.Stat(outFile)
	fmt.Fprintf(os.Stderr, "wrote %d matches, %d players → %s (%d bytes)\n",
		len(matchRecords), len(names), outFile, fi.Size())

	// Print top-20 ELO standings to stdout for quick sanity check.
	printStandings(rawMatches, birthDates)
}

// nopCloser wraps os.File to satisfy the Close interface for non-gzip path.
type nopCloser struct{ *os.File }

func (n *nopCloser) Close() error { return n.File.Close() }

// ── SQL parsing ──────────────────────────────────────────────────────────────

// parseResults reads a MariaDB dump of the `results` table and returns valid matches.
// Columns: winner(0), loser(1), round(2), score(3), tour(4), city(5), event(6),
//          date(7), source(8), season(9), notes(10), tmp_date(11)
func parseResults(path string) (matches []rawMatch, dupes int) {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	inValues := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "INSERT INTO `results` VALUES") {
			inValues = true
			continue
		}
		if inValues {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || trimmed == ";" {
				inValues = false
				continue
			}
			// Each data line starts with '(' and ends with '),' or ');'
			if !strings.HasPrefix(trimmed, "(") {
				continue
			}
			fields, ok := parseSQLRow(trimmed)
			if !ok || len(fields) < 12 {
				continue
			}
			winner := fields[0]
			loser := fields[1]
			tmpDate := fields[11]

			// Skip unknown opponents.
			if winner == "?" || loser == "?" || winner == "" || loser == "" {
				continue
			}
			// Skip rows without a parsed date.
			if tmpDate == "" || tmpDate == "NULL" {
				continue
			}
			// Validate date format loosely.
			if len(tmpDate) != 10 {
				continue
			}

			matches = append(matches, rawMatch{
				Date:   tmpDate,
				Winner: normalizeName(winner),
				Loser:  normalizeName(loser),
			})
		}
	}
	return matches, 0
}

// parseBirthDates reads player_profiles.sql and returns a map of player name → ISO birth date.
// Columns: player(0), gender(1), ..., dob(15), ..., tmp_dob(29)
func parseBirthDates(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	result := map[string]string{}
	inValues := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "INSERT INTO `player_profile` VALUES") {
			inValues = true
			continue
		}
		if inValues {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || trimmed == ";" {
				inValues = false
				continue
			}
			if !strings.HasPrefix(trimmed, "(") {
				continue
			}
			fields, ok := parseSQLRow(trimmed)
			if !ok || len(fields) < 16 {
				continue
			}
			name := fields[0]
			dob := fields[15] // M/D/YYYY

			if name == "" || name == "Player" || dob == "" || dob == "NULL" {
				continue
			}

			parsed, err := parseDate(dob)
			if err != nil {
				continue
			}
			result[normalizeName(name)] = parsed
		}
	}
	return result
}

// parseSQLRow parses one SQL value row like:
//   ('val1','val2',42,'val3',NULL,'escaped\'val'),...
// and returns the field values (unquoted, unescaped).
func parseSQLRow(line string) (fields []string, ok bool) {
	// Strip leading '(' and trailing '),' or ');'
	s := line
	if len(s) > 0 && s[0] == '(' {
		s = s[1:]
	}
	// Remove trailing comma and semicolon after the closing paren.
	for len(s) > 0 && (s[len(s)-1] == ',' || s[len(s)-1] == ';' || s[len(s)-1] == ')') {
		s = s[:len(s)-1]
	}

	var cur strings.Builder
	inQuote := false
	i := 0
	for i < len(s) {
		c := s[i]
		if inQuote {
			if c == '\\' && i+1 < len(s) {
				// Escape sequence: \' \\ etc.
				next := s[i+1]
				switch next {
				case '\'':
					cur.WriteByte('\'')
				case '\\':
					cur.WriteByte('\\')
				case 'n':
					cur.WriteByte('\n')
				case 'r':
					cur.WriteByte('\r')
				default:
					cur.WriteByte(next)
				}
				i += 2
				continue
			} else if c == '\'' {
				inQuote = false
				i++
				continue
			} else {
				cur.WriteByte(c)
			}
		} else {
			if c == '\'' {
				inQuote = true
				i++
				continue
			} else if c == ',' {
				fields = append(fields, cur.String())
				cur.Reset()
				i++
				continue
			} else if c == 'N' && strings.HasPrefix(s[i:], "NULL") {
				// NULL value
				fields = append(fields, "NULL")
				cur.Reset()
				i += 4
				// skip trailing comma
				if i < len(s) && s[i] == ',' {
					i++
				}
				continue
			}
			// Integer or other unquoted token — collect until comma
			cur.WriteByte(c)
		}
		i++
	}
	// Last field
	if last := cur.String(); last != "" || len(fields) > 0 {
		fields = append(fields, last)
	}
	return fields, true
}

// parseDate parses M/D/YYYY into YYYY-MM-DD.
func parseDate(s string) (string, error) {
	t, err := time.Parse("1/2/2006", s)
	if err != nil {
		t, err = time.Parse("01/02/2006", s)
		if err != nil {
			return "", err
		}
	}
	return t.Format("2006-01-02"), nil
}

// normalizeName title-cases each word in a name (handles "Last, First" format).
func normalizeName(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) == 0 {
			continue
		}
		runes := []rune(w)
		runes[0] = unicode.ToUpper(runes[0])
		for j := 1; j < len(runes); j++ {
			runes[j] = unicode.ToLower(runes[j])
		}
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}

// ── ELO sanity check ─────────────────────────────────────────────────────────

type playerStat struct {
	name    string
	elo     float64
	peakElo float64
	wins    int
	losses  int
	games   int
	lastDate string
}

// printStandings computes age-adjusted ELO and prints top-20 to stdout.
func printStandings(matches []rawMatch, birthDates map[string]string) {
	elo := map[string]float64{}
	peak := map[string]float64{}
	wins := map[string]int{}
	losses := map[string]int{}
	games := map[string]int{}
	lastDate := map[string]string{}

	const kHigh = 40.0
	const kMid = 20.0
	const kLow = 10.0
	const startElo = 1500.0

	getElo := func(name string) float64 {
		if e, ok := elo[name]; ok {
			return e
		}
		elo[name] = startElo
		peak[name] = startElo
		return startElo
	}

	kFactor := func(name string) float64 {
		g := games[name]
		e := elo[name]
		if g < 30 {
			return kHigh
		}
		if e < 2400 {
			return kMid
		}
		return kLow
	}

	for _, m := range matches {
		ea := getElo(m.Winner)
		eb := getElo(m.Loser)

		// Age adjustment: compute each player's age in days at match time.
		adjA, adjB := ageAdjustment(m.Winner, m.Loser, m.Date, birthDates)
		effA := ea + adjA
		effB := eb + adjB

		// Expected scores using age-adjusted effective ELOs.
		expA := 1.0 / (1.0 + math.Pow(10, (effB-effA)/400))
		expB := 1.0 - expA

		ka := kFactor(m.Winner)
		kb := kFactor(m.Loser)

		elo[m.Winner] = ea + ka*(1-expA)
		elo[m.Loser] = eb + kb*(0-expB)

		if elo[m.Winner] > peak[m.Winner] {
			peak[m.Winner] = elo[m.Winner]
		}
		if elo[m.Loser] > peak[m.Loser] {
			peak[m.Loser] = elo[m.Loser]
		}

		wins[m.Winner]++
		losses[m.Loser]++
		games[m.Winner]++
		games[m.Loser]++

		if m.Date > lastDate[m.Winner] {
			lastDate[m.Winner] = m.Date
		}
		if m.Date > lastDate[m.Loser] {
			lastDate[m.Loser] = m.Date
		}
	}

	// Build list and sort by ELO.
	var players []playerStat
	for name, e := range elo {
		players = append(players, playerStat{
			name:     name,
			elo:      e,
			peakElo:  peak[name],
			wins:     wins[name],
			losses:   losses[name],
			games:    games[name],
			lastDate: lastDate[name],
		})
	}
	sort.Slice(players, func(i, j int) bool {
		return players[i].elo > players[j].elo
	})

	fmt.Printf("\n%-30s  %7s  %7s  %5s  %5s  %5s  %s\n",
		"Player", "ELO", "Peak", "W", "L", "G", "Last Match")
	fmt.Println(strings.Repeat("-", 80))
	limit := 20
	if len(players) < limit {
		limit = len(players)
	}
	for i, p := range players[:limit] {
		fmt.Printf("%2d. %-27s  %7.0f  %7.0f  %5d  %5d  %5d  %s\n",
			i+1, p.name, p.elo, p.peakElo, p.wins, p.losses, p.games, p.lastDate)
	}
	fmt.Printf("\nTotal players: %d  Total matches: %d\n", len(players), len(matches))
}

// ageAdjustment returns (adjA, adjB): effective ELO delta for winner/loser at matchDate.
// For each 200 days player A is older than player B, A gets -1 and B gets +1.
func ageAdjustment(nameA, nameB, matchDate string, birthDates map[string]string) (adjA, adjB float64) {
	dobA, okA := birthDates[nameA]
	dobB, okB := birthDates[nameB]
	if !okA || !okB {
		return 0, 0
	}
	matchT, err := time.Parse("2006-01-02", matchDate)
	if err != nil {
		return 0, 0
	}
	birthA, err := time.Parse("2006-01-02", dobA)
	if err != nil {
		return 0, 0
	}
	birthB, err := time.Parse("2006-01-02", dobB)
	if err != nil {
		return 0, 0
	}
	ageADays := matchT.Sub(birthA).Hours() / 24
	ageBDays := matchT.Sub(birthB).Hours() / 24
	// age_diff: positive means A is older than B
	ageDiff := ageADays - ageBDays
	adj := ageDiff / 200.0
	// Older player (A) gets -adj, younger (B) gets +adj
	return -adj, adj
}
