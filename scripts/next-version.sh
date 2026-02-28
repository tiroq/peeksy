#!/usr/bin/env bash
# scripts/next-version.sh
#
# Reads the latest semver git tag and the commits since it, then prints the
# next version (vMAJOR.MINOR.PATCH) according to Conventional Commits rules:
#
#   BREAKING CHANGE (footer) or feat!(...) or fix!(...) → bump MAJOR
#   feat(...)                                            → bump MINOR
#   anything else (fix, chore, refactor, docs, …)        → bump PATCH
#
# If no tags exist at all, starts from v0.0.0.
# Exits with the version string on stdout and nothing else.
#
# Usage:
#   next_tag=$(bash scripts/next-version.sh)
#   next_tag=$(bash scripts/next-version.sh patch)   # force patch
#   next_tag=$(bash scripts/next-version.sh minor)   # force minor
#   next_tag=$(bash scripts/next-version.sh major)   # force major
set -euo pipefail

# ── 1. Find latest semver tag ──────────────────────────────────────────────────

LATEST=$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' \
           --sort=-v:refname 2>/dev/null | head -1)

if [[ -z "$LATEST" ]]; then
  LATEST="v0.0.0"
fi

# Strip leading 'v'
VERSION="${LATEST#v}"

IFS='.' read -r MAJOR MINOR PATCH <<< "$VERSION"
MAJOR=${MAJOR:-0}
MINOR=${MINOR:-0}
PATCH=${PATCH:-0}

# ── 2. Determine bump level ────────────────────────────────────────────────────

FORCED="${1:-}"   # optional first argument: major | minor | patch

if [[ -n "$FORCED" ]]; then
  BUMP="$FORCED"
else
  # Collect all commits since the last tag (or all commits if no tag existed)
  if [[ "$LATEST" == "v0.0.0" ]]; then
    COMMITS=$(git log --pretty=format:"%s%n%b" 2>/dev/null || true)
  else
    COMMITS=$(git log "${LATEST}..HEAD" --pretty=format:"%s%n%b" 2>/dev/null || true)
  fi

  BUMP="patch"   # default

  # Walk every line; first match at the highest level wins
  while IFS= read -r line; do
    # BREAKING CHANGE in body/footer
    if [[ "$line" == BREAKING\ CHANGE* || "$line" == BREAKING-CHANGE* ]]; then
      BUMP="major"
      break
    fi
    # feat!... fix!... refactor!... etc. (breaking via bang)
    # Strip optional scope then check for '!'
    subject="${line%%:*}"   # everything before first colon
    if [[ "$subject" == *! ]]; then
      BUMP="major"
      break
    fi
    # feat: or feat(...): → minor (use case to avoid [[ ]] glob issues with parens)
    if [[ "$BUMP" != "major" ]]; then
      case "$line" in
        feat:*|feat\(*) BUMP="minor" ;;
      esac
    fi
  done <<< "$COMMITS"
fi

# ── 3. Apply bump ──────────────────────────────────────────────────────────────

case "$BUMP" in
  major)
    MAJOR=$((MAJOR + 1))
    MINOR=0
    PATCH=0
    ;;
  minor)
    MINOR=$((MINOR + 1))
    PATCH=0
    ;;
  patch)
    PATCH=$((PATCH + 1))
    ;;
  *)
    echo "ERROR: unknown bump level '$BUMP'. Use major|minor|patch." >&2
    exit 1
    ;;
esac

echo "v${MAJOR}.${MINOR}.${PATCH}"
