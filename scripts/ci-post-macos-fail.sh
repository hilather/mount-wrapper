#!/usr/bin/env bash
# Post macOS go-test failure details as a public commit comment.
# Used by .github/workflows/ci.yml (GITHUB_TOKEN + contents:write).
set -uo pipefail

body_file="${1:-/tmp/macos-fail.md}"
if [[ ! -s "$body_file" ]]; then
  echo "macOS go test failed (no package log captured)" >"$body_file"
  if [[ -f /tmp/macos-gotest.log ]]; then
    {
      echo '```'
      tail -n 200 /tmp/macos-gotest.log
      echo '```'
    } >>"$body_file"
  fi
fi

head -c 60000 "$body_file" > /tmp/macos-fail-trim.md

python3 - <<'PY'
import json
import os
import urllib.error
import urllib.request

body = open("/tmp/macos-fail-trim.md", encoding="utf-8").read()
repo = os.environ["GITHUB_REPOSITORY"]
sha = os.environ["GITHUB_SHA"]
token = os.environ["GH_TOKEN"]
req = urllib.request.Request(
    f"https://api.github.com/repos/{repo}/commits/{sha}/comments",
    data=json.dumps({"body": body}).encode(),
    headers={
        "Authorization": f"Bearer {token}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "Content-Type": "application/json",
        "User-Agent": "mount-wrapper-ci",
    },
    method="POST",
)
try:
    with urllib.request.urlopen(req) as resp:
        print("comment status", resp.status)
except urllib.error.HTTPError as e:
    print("comment HTTPError", e.code, e.read()[:500])
    raise
PY
