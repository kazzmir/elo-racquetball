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
import gzip

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


def age_adjustment(name_a: str, name_b: str, match_date: str, birth_dates: dict) -> tuple[float, float]:
    """Return (adj_a, adj_b): effective ELO delta based on age difference at match_date.
    For every 200 days player A is older than player B, A gets -1 effective ELO."""
    dob_a = birth_dates.get(name_a)
    dob_b = birth_dates.get(name_b)
    if not dob_a or not dob_b:
        return 0.0, 0.0
    from datetime import date
    def parse(s):
        return date.fromisoformat(s)
    match = parse(match_date)
    age_a = (match - parse(dob_a)).days
    age_b = (match - parse(dob_b)).days
    age_diff = age_a - age_b  # positive → A is older
    adj = age_diff / 200.0
    return -adj, adj  # older gets negative


def compute_elo(matches: list[dict], birth_dates: dict | None = None) -> dict:
    """Return a dict of player -> {elo, wins, losses} after processing all matches."""
    if birth_dates is None:
        birth_dates = {}
    ratings: dict[str, dict] = {}

    def get(name):
        if name not in ratings:
            ratings[name] = {"elo": INITIAL_ELO, "wins": 0, "losses": 0}
        return ratings[name]

    for m in matches:
        w, l = get(m["winner"]), get(m["loser"])
        adj_w, adj_l = age_adjustment(m["winner"], m["loser"], m["date"], birth_dates)
        ea = expected_score(w["elo"] + adj_w, l["elo"] + adj_l)
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
        opener = gzip.open if output_json.endswith('.gz') else open
        with opener(output_json, 'rt') as f:
            data = json.load(f)
    except FileNotFoundError:
        sys.exit(f"Error: {output_json} not found. Provide HTML files to generate it.")
    except json.JSONDecodeError as e:
        sys.exit(f"Error reading {output_json}: {e}")

    # Support both old format (array) and new format ({matches, players}).
    if isinstance(data, list):
        matches, birth_dates = data, {}
    else:
        matches = data.get("matches", [])
        birth_dates = data.get("players", {})

    ratings = compute_elo(matches, birth_dates)
    print_standings(ratings, len(matches))


if __name__ == "__main__":
    main()
