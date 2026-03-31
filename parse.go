package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type Match struct {
	Date   string `json:"date"`
	Winner string `json:"winner"`
	Loser  string `json:"loser"`
}

func main() {
	// Usage: parse <output.json> <input1.html> [input2.html ...]
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <output.json> <input1.html> [input2.html ...]\n", os.Args[0])
		os.Exit(1)
	}

	outputPath := os.Args[1]
	inputPaths := os.Args[2:]

	// Use a map to deduplicate: key = "date|winner|loser"
	seen := make(map[string]struct{})
	var matches []Match

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
				matches = append(matches, m)
				added++
			}
		}
		fmt.Fprintf(os.Stderr, "%s: %d matches (%d new)\n", path, len(parsed), added)
	}

	// Sort chronologically by parsed date, preserving file order for same-day matches.
	sort.SliceStable(matches, func(i, j int) bool {
		return parseDate(matches[i].Date).Before(parseDate(matches[j].Date))
	})

	out, err := os.Create(outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(matches); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Total: %d matches written to %s\n", len(matches), outputPath)
}

func parseFile(path string) ([]Match, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	doc, err := html.Parse(f)
	if err != nil {
		return nil, err
	}

	var matches []Match
	var walk func(*html.Node)

	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			cells := extractCells(n)
			// Rows have 8 columns: Season, Date, City, Event, Round, Winner, Loser, Score
			if len(cells) >= 7 {
				date := strings.TrimSpace(cells[1])
				winner := strings.TrimSpace(cells[5])
				loser := strings.TrimSpace(cells[6])

				// Skip header row, unknown opponents, or empty players
				if date == "Date" || winner == "" || loser == "" || loser == "?" || winner == "?" {
					return
				}

				if parseDate(date).IsZero() {
					return
				}

				matches = append(matches, Match{
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
