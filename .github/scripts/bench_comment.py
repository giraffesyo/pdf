#!/usr/bin/env python3
"""Render a benchstat CSV comparison as a Markdown comment body.

Usage: bench_comment.py <benchstat-csv> <raw-benchstat-text>

The CSV comes from `benchstat -format csv base.txt head.txt`: one block
per metric, each with a header row naming the metric and one row per
benchmark, ending in a geomean row. Environment variables supply the
links that make a rewritten comment obviously current.
"""

import csv
import os
import sys

MARKER = "<!-- benchmark-comparison -->"
SIGNIFICANT = 10.0  # percent; below this hosted runners are too noisy to call


def read_blocks(path):
    """Split the CSV into (metric, rows) blocks, one per measurement."""
    with open(path, newline="") as f:
        rows = list(csv.reader(f))
    blocks, metric, body = [], None, []
    for row in rows:
        if not any(cell.strip() for cell in row):
            if metric:
                blocks.append((metric, body))
            metric, body = None, []
            continue
        if row and row[0] == "" and len(row) > 1:
            if row[1].endswith("/op"):  # header naming the metric
                metric, body = row[1], []
            continue  # the other all-empty-first-cell row names the files
        if metric:
            body.append(row)
    if metric:
        blocks.append((metric, body))
    return blocks


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


def fmt_count(n):
    return f"{n:,.0f}"


FORMATTERS = {"sec/op": fmt_time, "B/op": fmt_bytes, "allocs/op": fmt_count}


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
    arrow = "🟢" if percent < 0 else "🔴"
    if metric != "sec/op" and percent > 0:
        arrow = "🟡"  # more memory is a trade-off, not a failure
    return f"{arrow} **{display}**"


def strip_suffix(name):
    """Drop the -N that Go appends for GOMAXPROCS."""
    head, sep, tail = name.rpartition("-")
    return head if sep and tail.isdigit() else name


def collect(blocks):
    """Merge the per-metric blocks into one row per benchmark, in order."""
    names, data = [], {}
    for metric, rows in blocks:
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
    return names, data


def main():
    csv_path, raw_path = sys.argv[1], sys.argv[2]
    names, data = collect(read_blocks(csv_path))
    metrics = [m for m in ("sec/op", "B/op", "allocs/op") if any(m in data[n] for n in names)]

    base_ref = os.environ.get("BASE_REF", "the base branch")
    out = [
        MARKER,
        f"### Benchmarks vs `{base_ref}`",
        "",
    ]
    if not names:
        out += ["_No benchmark results were produced._", ""]
    else:
        header = "| Benchmark | " + " | ".join(
            f"{m} before | after | Δ" for m in metrics
        ) + " |"
        align = "|:--|" + "".join("--:|--:|--:|" for _ in metrics)
        out += [header, align]
        for name in names:
            label = f"**{name}**" if name == "geomean" else f"`{name}`"
            cells = [label]
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

    commit = os.environ.get("HEAD_SHA", "")[:7]
    run_url = os.environ.get("RUN_URL", "")
    stamp = os.environ.get("STAMP", "")
    provenance = "Measured on this run's runner, one run per side"
    if commit:
        provenance += f", commit `{commit}`"
    if run_url:
        provenance += f" ([run]({run_url}))"
    if stamp:
        provenance += f", updated {stamp}"
    out += [
        f"_{provenance}. Differences under {SIGNIFICANT:.0f}% are within the noise "
        "of a shared runner and are shown as `~`. This comment is rewritten in "
        "place on every push, so it always reflects the latest commit._",
        "",
        "<details><summary>Raw benchstat output</summary>",
        "",
        "```",
        open(raw_path).read().rstrip(),
        "```",
        "",
        "</details>",
    ]
    sys.stdout.write("\n".join(out) + "\n")


if __name__ == "__main__":
    main()
