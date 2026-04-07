package main

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

type rawMatch struct {
	Date   string
	Winner string
	Loser  string
}

// Output is the JSON structure written to disk.
// names[i] is the player name; births[i] is always "" for HTML-parsed data (no profile info).
type Output struct {
	Names   []string       `json:"names"`
	Births  []string       `json:"births"`
	Matches []MatchRecord  `json:"matches"`
}

type MatchRecord struct {
	Date   string `json:"date"`
	Winner int    `json:"winner"`
	Loser  int    `json:"loser"`
}

func main() {
	// Usage: parse <output.json> <input1.html> [input2.html ...]
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <output.json> <input1.html> [input2.html ...]\n", os.Args[0])
		os.Exit(1)
	}

	outputPath := os.Args[1]
	inputPaths := os.Args[2:]

	seen := make(map[string]struct{})
	var rawMatches []rawMatch

	for _, path := range inputPaths {
		parsed, err := parseFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %v\n", path, err)
			os.Exit(1)
		}
		added := 0
		for _, m := range parsed {
			key := m.Date + "|" + m.Winner + "|" + m.Loser
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				rawMatches = append(rawMatches, m)
				added++
			}
		}
		fmt.Fprintf(os.Stderr, "%s: %d matches (%d new)\n", path, len(parsed), added)
	}

	// Sort chronologically by parsed date, preserving file order for same-day matches.
	sort.SliceStable(rawMatches, func(i, j int) bool {
		return parseDate(rawMatches[i].Date).Before(parseDate(rawMatches[j].Date))
	})

	// Build player index: assign integer IDs in order of first appearance.
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
	for _, m := range rawMatches {
		playerID(m.Winner)
		playerID(m.Loser)
	}

	// births is all empty strings for HTML-parsed data (no birth date info).
	births := make([]string, len(names))

	matchRecords := make([]MatchRecord, len(rawMatches))
	for i, m := range rawMatches {
		matchRecords[i] = MatchRecord{
			Date:   m.Date,
			Winner: nameIndex[m.Winner],
			Loser:  nameIndex[m.Loser],
		}
	}

	output := Output{
		Names:   names,
		Births:  births,
		Matches: matchRecords,
	}

	out, err := os.Create(outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()

	var w io.Writer = out
	if strings.HasSuffix(outputPath, ".gz") {
		gz := gzip.NewWriter(out)
		defer gz.Close()
		w = gz
	}

	enc := json.NewEncoder(w)
	if err := enc.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Total: %d matches, %d players written to %s\n", len(matchRecords), len(names), outputPath)
}

func parseFile(path string) ([]rawMatch, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	doc, err := html.Parse(f)
	if err != nil {
		return nil, err
	}

	var matches []rawMatch
	var walk func(*html.Node)

	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			cells := extractCells(n)
			// Rows have 8 columns: Season, Date, City, Event, Round, Winner, Loser, Score
			if len(cells) >= 7 {
				date := strings.TrimSpace(cells[1])
				winner := normalizeName(cells[5])
				loser := normalizeName(cells[6])

				// Skip header row, unknown opponents, or empty players
				if date == "Date" || winner == "" || loser == "" || loser == "?" || winner == "?" {
					return
				}

				if parseDate(date).IsZero() {
					return
				}

				matches = append(matches, rawMatch{
					Date:   date,
					Winner: winner,
					Loser:  loser,
				})
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}

	walk(doc)
	return matches, nil
}

func parseDate(s string) time.Time {
	for _, layout := range []string{"1/2/2006", "01/02/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// normalizeName lowercases then title-cases each space-separated word,
// so "bob jones" and "BOB JONES" both become "Bob Jones".
func normalizeName(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		runes := []rune(w)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
		}
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}

// extractCells returns the text content of all <td> children of a <tr> node.
func extractCells(tr *html.Node) []string {
	var cells []string
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "td" {
			cells = append(cells, textContent(c))
		}
	}
	return cells
}

// textContent recursively extracts all text from an HTML node.
func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(textContent(c))
	}
	return sb.String()
}
