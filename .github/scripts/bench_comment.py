#!/usr/bin/env python3
"""Render a benchstat CSV comparison as a Markdown comment body.

Usage: bench_comment.py <benchstat-csv> <raw-benchstat-text>

The CSV comes from `benchstat -format csv base=... head=...`. It opens
with the configuration the benchmarks ran under and then repeats, once
per package, a block per metric: a header row naming the metric, a row
per benchmark, and a geomean. Environment variables supply the links and
timestamp that make a rewritten comment obviously current.
"""

import csv
import os
import sys

MARKER = "<!-- benchmark-comparison -->"
SIGNIFICANT = 10.0  # percent; below this a shared runner cannot tell the difference
CONFIG_KEYS = ("goos", "goarch", "pkg", "cpu")
METRIC_ORDER = ("sec/op", "B/op", "allocs/op")


def read_groups(path):
    """Return (config, groups): one group per package, each metric's rows."""
    with open(path, newline="") as f:
        rows = list(csv.reader(f))
    config, groups, current = {}, [], None
    for row in rows:
        if not any(cell.strip() for cell in row):
            continue
        first = row[0]
        key = first.split(":", 1)[0].strip()
        if key in CONFIG_KEYS and ":" in first:
            value = first.split(":", 1)[1].strip()
            config.setdefault(key, value)
            if key == "pkg":
                current = {"pkg": value, "metrics": []}
                groups.append(current)
            continue
        if first == "" and len(row) > 1:
            if row[1].endswith("/op"):  # header naming this block's metric
                if current is None:
                    current = {"pkg": "", "metrics": []}
                    groups.append(current)
                current["metrics"].append((row[1], []))
            continue  # the other leading-empty row names the two inputs
        if current and current["metrics"]:
            current["metrics"][-1][1].append(row)
    return config, groups


def sig3(value):
    """Three significant digits, without a trailing decimal point."""
    return f"{value:#.3g}".rstrip(".")


def fmt_time(seconds):
    for scale, unit in ((1.0, "s"), (1e-3, "ms"), (1e-6, "µs"), (1e-9, "ns")):
        if seconds >= scale:
            return f"{sig3(seconds / scale)} {unit}"
    return f"{sig3(seconds * 1e9)} ns"


def fmt_bytes(n):
    for scale, unit in ((1 << 30, "GB"), (1 << 20, "MB"), (1 << 10, "KB")):
        if n >= scale:
            return f"{sig3(n / scale)} {unit}"
    return f"{n:.0f} B"


FORMATTERS = {"sec/op": fmt_time, "B/op": fmt_bytes, "allocs/op": lambda n: f"{n:,.0f}"}


def parse_delta(text):
    """Return (display, percent) for benchstat's "vs base" cell."""
    text = text.strip()
    if not text or text == "~":
        return "~", 0.0
    try:
        return text, float(text.rstrip("%"))
    except ValueError:
        return text, 0.0


def decorate(display, percent, metric):
    """Mark differences big enough to be worth a reader's attention."""
    if abs(percent) < SIGNIFICANT:
        return display
    if percent < 0:
        return f"🟢 **{display}**"
    # More memory is a trade-off worth seeing, not a failure like slower is.
    return f"{'🔴' if metric == 'sec/op' else '🟡'} **{display}**"


def strip_suffix(name):
    """Drop the -N that Go appends for GOMAXPROCS."""
    head, sep, tail = name.rpartition("-")
    return head if sep and tail.isdigit() else name


def collect(group):
    """Merge a package's per-metric blocks into one row per benchmark."""
    names, data = [], {}
    for metric, rows in group["metrics"]:
        for row in rows:
            if len(row) < 6:
                continue
            name = strip_suffix(row[0])
            if name not in data:
                names.append(name)
                data[name] = {}
            try:
                base, head = float(row[1]), float(row[3])
            except ValueError:
                continue
            data[name][metric] = (base, head, *parse_delta(row[5]))
    metrics = [m for m in METRIC_ORDER if any(m in data[n] for n in names)]
    return names, data, metrics


def render_table(group, show_heading):
    names, data, metrics = collect(group)
    if not names:
        return []
    out = []
    if show_heading:
        out += [f"**{group['pkg']}**", ""]
    out.append("| Benchmark | " + " | ".join(f"{m} before | after | Δ" for m in metrics) + " |")
    out.append("|:--|" + "".join("--:|--:|--:|" for _ in metrics))
    for name in names:
        cells = [f"**{name}**" if name == "geomean" else f"`{name}`"]
        for metric in metrics:
            entry = data[name].get(metric)
            if not entry:
                cells += ["", "", ""]
                continue
            base, head, display, percent = entry
            fmt = FORMATTERS.get(metric, str)
            cells += [fmt(base), fmt(head), decorate(display, percent, metric)]
        out.append("| " + " | ".join(cells) + " |")
    out.append("")
    return out


def main():
    csv_path, raw_path = sys.argv[1], sys.argv[2]
    config, groups = read_groups(csv_path)
    base_ref = os.environ.get("BASE_REF", "the base branch")

    body = [MARKER, f"### Benchmarks vs `{base_ref}`", ""]
    tables = [render_table(g, show_heading=len(groups) > 1) for g in groups]
    tables = [t for t in tables if t]
    if not tables:
        body += ["_No benchmark results were produced._", ""]
    for table in tables:
        body += table

    provenance = ["one run per side"]
    if config.get("cpu"):
        provenance.append(f"on {config['cpu']}")
    if commit := os.environ.get("HEAD_SHA", "")[:7]:
        provenance.append(f"commit `{commit}`")
    if run_url := os.environ.get("RUN_URL", ""):
        provenance.append(f"[run]({run_url})")
    if stamp := os.environ.get("STAMP", ""):
        provenance.append(f"updated {stamp}")
    body += [
        f"_Measured {', '.join(provenance)}. Differences under {SIGNIFICANT:.0f}% are "
        "within a shared runner's noise and read as `~`. This comment is rewritten in "
        "place on every push — check the timestamp, not its position in the timeline._",
        "",
        "<details><summary>Raw benchstat output</summary>",
        "",
        "```",
        open(raw_path).read().rstrip(),
        "```",
        "",
        "</details>",
    ]
    sys.stdout.write("\n".join(body) + "\n")


if __name__ == "__main__":
    main()
