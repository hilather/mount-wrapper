#!/usr/bin/env python3
"""Parse tarmount-wsl cli.py add_parser names for parity inventory."""
from __future__ import annotations

import re
import sys


def main() -> int:
    if len(sys.argv) < 2:
        print("usage: parse_upstream_cli.py CLI_PY", file=sys.stderr)
        return 2
    text = open(sys.argv[1], encoding="utf-8").read()
    cmds: list[str] = []
    for m in re.finditer(r"""add_parser\(\s*['"]([^'"]+)['"]""", text):
        cmds.append(m.group(1))
    flat: list[str] = []
    has = set(cmds)
    if "config" in has:
        flat.append("config show")
        flat.append("config set")
    if "hooks" in has:
        flat.append("hooks list")
        flat.append("hooks status")
    for c in (
        "version",
        "serve",
        "doctor",
        "status",
        "metrics",
        "rescan",
        "retry",
        "web",
        "mount",
        "unmount",
        "purge",
    ):
        if c in has:
            flat.append(c)
    for c in flat:
        print(c)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
