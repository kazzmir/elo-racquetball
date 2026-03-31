#!/usr/bin/env python3
"""
ELO standings script for Pro Racquetball.

Usage:
  python3 elo.py <matches.json>
  python3 elo.py <matches.json> <player1.html> [player2.html ...]

If HTML files are provided, parse.go is invoked first to regenerate
matches.json from the given HTML files (with automatic deduplication).
The ELO standings are then computed and printed.
"""

import json
import math
import subprocess
import sys
from pathlib import Path

INITIAL_ELO = 1000


def k_factor(elo: int, games: int) -> int:
    """FIDE-style K factor: 40 for new players, 20 below 2400, 10 above."""
    if games < 30:
        return 40
    if elo < 2400:
        return 20
    return 10


def expected_score(ra: int, rb: int) -> float:
    return 1 / (1 + math.pow(10, (rb - ra) / 400))


def compute_elo(matches: list[dict]) -> dict:
    """Return a dict of player -> {elo, wins, losses} after processing all matches."""
    ratings: dict[str, dict] = {}

    def get(name):
        if name not in ratings:
            ratings[name] = {"elo": INITIAL_ELO, "wins": 0, "losses": 0}
        return ratings[name]

    for m in matches:
        w, l = get(m["winner"]), get(m["loser"])
        ea = expected_score(w["elo"], l["elo"])
        kw = k_factor(w["elo"], w["wins"] + w["losses"])
        kl = k_factor(l["elo"], l["wins"] + l["losses"])
        w["elo"] = round(w["elo"] + kw * (1 - ea))
        l["elo"] = round(l["elo"] + kl * (0 - (1 - ea)))
        w["wins"] += 1
        l["losses"] += 1

    return ratings


def parse_html(output_json: str, html_files: list[str]) -> None:
    """Invoke parse.go to regenerate matches.json from the given HTML files."""
    script_dir = Path(__file__).parent
    parse_go = script_dir / "parse.go"
    if not parse_go.exists():
        sys.exit(f"Error: parse.go not found at {parse_go}")

    cmd = ["go", "run", str(parse_go), output_json] + html_files
    result = subprocess.run(cmd, cwd=script_dir, capture_output=True, text=True)
    if result.returncode != 0:
        sys.exit(f"parse.go failed:\n{result.stderr}")
    # Print parse progress to stderr so it doesn't mix with standings output
    print(result.stderr, end="", file=sys.stderr)


def print_standings(ratings: dict, match_count: int) -> None:
    sorted_players = sorted(ratings.items(), key=lambda x: -x[1]["elo"])

    col_name = max(len(name) for name in ratings) + 2
    header = (
        f"{'#':>4}  {'Player':<{col_name}}  {'ELO':>6}  {'W':>5}  {'L':>5}  {'Win%':>6}  {'Games':>5}"
    )
    sep = "-" * len(header)

    print(f"\n{match_count} matches · {len(ratings)} players\n")
    print(header)
    print(sep)

    for rank, (name, p) in enumerate(sorted_players, 1):
        games = p["wins"] + p["losses"]
        pct = f"{p['wins'] / games * 100:.1f}%" if games else "—"
        print(
            f"{rank:>4}  {name:<{col_name}}  {p['elo']:>6}  {p['wins']:>5}  {p['losses']:>5}  {pct:>6}  {games:>5}"
        )


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)

    output_json = sys.argv[1]
    html_files = sys.argv[2:]

    if html_files:
        parse_html(output_json, html_files)

    try:
        with open(output_json) as f:
            matches = json.load(f)
    except FileNotFoundError:
        sys.exit(f"Error: {output_json} not found. Provide HTML files to generate it.")
    except json.JSONDecodeError as e:
        sys.exit(f"Error reading {output_json}: {e}")

    ratings = compute_elo(matches)
    print_standings(ratings, len(matches))


if __name__ == "__main__":
    main()
