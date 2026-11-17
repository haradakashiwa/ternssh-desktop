#!/bin/sh
# Makes again the disk images mygo build made in the build directories
# given, as build/darwin-universal, with the background of
# resources/dmg-background.png: mygo's have a white one.
#
#   scripts/dmg.sh build/darwin-universal
#
# dmgbuild comes from PATH, or is installed in build/.dmgbuild.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
dmgbuild=$(command -v dmgbuild || true)
if [ -z "$dmgbuild" ]; then
	venv="$root/build/.dmgbuild"
	if [ ! -x "$venv/bin/dmgbuild" ]; then
		python3 -m venv "$venv"
		PIP_DISABLE_PIP_VERSION_CHECK=1 "$venv/bin/pip" install -q dmgbuild==2.0.0
	fi
	dmgbuild="$venv/bin/dmgbuild"
fi

for dir in "$@"; do
	app=$(ls -d "$dir"/*.app | head -n 1)
	for dmg in "$dir"/*.dmg; do
		[ -e "$dmg" ] || { echo "no .dmg in $dir" >&2; exit 1; }
		volume=$(basename "$app" .app)
		"$dmgbuild" -s "$root/scripts/dmg_settings.py" \
			-D app="$app" -D root="$root" \
			"$volume" "$dmg"
	done
done
