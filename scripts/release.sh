#!/usr/bin/env bash
# Cut a release: bump every version, regenerate the spec, test, commit and tag.
#
#   scripts/release.sh 0.2.0        # or: make release V=0.2.0
#
# Pushing the tag (printed at the end) runs .github/workflows/release.yml, which builds
# the binaries, publishes a GitHub Release and the npm package.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

V="${1:-}"
[[ "$V" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || { echo "usage: $0 <semver, e.g. 0.2.0>" >&2; exit 2; }
[ -z "$(git status --porcelain)" ] || { echo "working tree not clean; commit or stash first" >&2; exit 1; }
[ "$(git rev-parse --abbrev-ref HEAD)" = "main" ] || { echo "release from main" >&2; exit 1; }
git rev-parse -q --verify "refs/tags/v$V" >/dev/null && { echo "tag v$V already exists" >&2; exit 1; }

echo "$V" > VERSION
(cd web && npm version "$V" --no-git-tag-version --allow-same-version >/dev/null)
python3 scripts/gen_openapi.py api/internal/server/openapi.json >/dev/null
make test

git add VERSION web/package.json web/package-lock.json api/internal/server/openapi.json
git commit -m "Release v$V"
git tag -a "v$V" -m "Oversight v$V"
echo
echo "Tagged v$V. Publish it with:"
echo "  git push origin main v$V"
