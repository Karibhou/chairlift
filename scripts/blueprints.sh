#!/usr/bin/env bash
# Compile internal/ui/*.blp into the GtkBuilder XML the binary embeds.
#
#   scripts/blueprints.sh          regenerate internal/ui/*.ui
#   scripts/blueprints.sh --check  fail if a committed .ui differs from its .blp
#
# The compiler runs inside the pinned Dakota image, never from the host: the
# generated XML depends on the blueprint-compiler release and on the GTK and
# libadwaita introspection data it validates against, so one image keeps every
# contributor and CI byte-identical. The Go build stays pure (CGO_ENABLED=0,
# no Python): it embeds the committed .ui files, and this script is the only
# step that writes them. See internal/ui/ui.go.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/test/e2e/dakota-image.sh"
UI="$ROOT/internal/ui"

check=0
case "${1:-}" in
    --check) check=1 ;;
    "") ;;
    *) echo "usage: $0 [--check]" >&2; exit 2 ;;
esac

command -v podman >/dev/null || { echo "podman is required" >&2; exit 1; }

# Always compile into a fresh directory: blueprint-compiler skips an output
# that looks newer than its source, which would keep a hand-edited .ui.
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

shopt -s nullglob
sources=("$UI"/*.blp)
[ "${#sources[@]}" -gt 0 ] || { echo "no blueprints under $UI" >&2; exit 1; }

chmod 0777 "$out"
podman run --rm --pull=missing --userns=keep-id --security-opt label=disable \
    -v "$UI:$UI:ro" -v "$out:$out" -w "$UI" "$IMAGE" \
    blueprint-compiler batch-compile "$out" "$UI" "${sources[@]}"

if [ "$check" = 1 ]; then
    status=0
    for blp in "${sources[@]}"; do
        ui="$(basename "${blp%.blp}").ui"
        if ! diff -u "$UI/$ui" "$out/$ui"; then
            echo "internal/ui/$ui is stale; run scripts/blueprints.sh (make blueprints)" >&2
            status=1
        fi
    done
    for ui in "$UI"/*.ui; do
        [ -f "${ui%.ui}.blp" ] || { echo "internal/ui/$(basename "$ui") has no .blp source" >&2; status=1; }
    done
    exit "$status"
fi

for blp in "${sources[@]}"; do
    ui="$(basename "${blp%.blp}").ui"
    cp "$out/$ui" "$UI/$ui"
done
for ui in "$UI"/*.ui; do
    [ -f "${ui%.ui}.blp" ] || { echo "removing $(basename "$ui"): its .blp is gone"; rm -f "$ui"; }
done
