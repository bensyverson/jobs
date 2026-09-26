#!/usr/bin/env bash
# Contact sheet for the dashboard's preview catalog: one PNG per
# component state, in both color schemes, via sleepy (headless WebKit).
#
# Usage:
#   scripts/preview-shots.sh <base-url> <out-dir> [component] [width]
#
# <base-url> is a running dashboard (`job serve`) or preview catalog
# (`job preview`, which needs no store),
# e.g. http://127.0.0.1:7823. Every state link on /preview (or on
# /preview/<component> when given) is shot as <out-dir>/<component>--<state>--<scheme>.png,
# cropped to the component's custom element when the page has one.
# width defaults to 1280 CSS px. Put <out-dir> under scripts/screenshots/
# (gitignored), e.g. scripts/screenshots/$(date +%F)-<component>.
#
# Look at every PNG: the contact sheet is the review, not the tests.

set -euo pipefail

base="${1:?base url}"
out="${2:?out dir}"
component="${3:-}"
width="${4:-1280}"
sleepy="${SLEEPY:-/Users/ben/.swiftpm/bin/sleepy}"

mkdir -p "$out"
index="$base/preview${component:+/$component}"
states=$(curl -fsS "$index" | grep -oE 'href="/preview/[a-z0-9-]+/[a-z0-9-]+"' | sed -E 's/^href="(.*)"$/\1/' | sort -u)

for path in $states; do
  comp=$(echo "$path" | cut -d/ -f3)
  state=$(echo "$path" | cut -d/ -f4)
  for scheme in dark light; do
    file="$out/$comp--$state--$scheme.png"
    "$sleepy" shot "$base$path" --selector "$comp" --size "${width}x900" --theme "$scheme" --out "$file"
    echo "$file"
  done
done
