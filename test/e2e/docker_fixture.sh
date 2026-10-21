#!/usr/bin/env bash
# Provision only the disposable rootless Podman systemd container. No host state.
set -euo pipefail
UID_NUMBER="${1:?fixture uid required}"
GID_NUMBER="${2:?fixture gid required}"
[ "$(id -u)" = 0 ] || { echo "fixture setup requires container root" >&2; exit 1; }
[ "$(cat /proc/1/comm)" = systemd ] || { echo "fixture requires native systemd PID 1" >&2; exit 1; }
for _ in {1..300}; do
    [ -S /run/systemd/private ] && break
    sleep 0.1
done
[ -S /run/systemd/private ] || { echo "fixture systemd manager did not become ready" >&2; exit 1; }
boot_state="$(timeout 90 systemctl is-system-running --wait)" || {
    [ "$boot_state" = degraded ] || { echo "fixture boot failed: $boot_state" >&2; exit 1; }
}
# Native boot can mount /home after Podman's initial bind mounts. Keep the
# checkout reachable at its original absolute path through a stable /opt bind.
ROOT="${3:?checkout path required}"
if [ ! -e "$ROOT" ]; then
    mkdir -p "$(dirname "$ROOT")"
    ln -s /opt/chairlift-checkout "$ROOT"
fi
setpriv --reuid="$UID_NUMBER" --regid="$GID_NUMBER" --clear-groups test -r "$ROOT/go.mod" || {
    stat "$ROOT" "$(dirname "$ROOT")" /opt/chairlift-checkout >&2
    echo "fixture checkout is not readable by its test account" >&2
    exit 1
}
ACCOUNT="$(getent passwd "$UID_NUMBER" | cut -d: -f1)"
[ -n "$ACCOUNT" ] || { echo "fixture account is missing" >&2; exit 1; }

# keep-id leaves a contiguous subordinate range after the invoking account.
# Dakota's native uidmap helpers carry file capabilities: do NOT add setuid.
for kind in uid gid; do
    start=0 count=0
    number="$UID_NUMBER"
    if [ "$kind" = gid ]; then number="$GID_NUMBER"; fi
    while read -r inner outer length; do
        if (( inner <= number && number < inner + length )); then
            length=$((inner + length - number - 1))
            inner=$((number + 1))
        fi
        if (( length > count )); then start="$inner"; count="$length"; fi
    done < "/proc/self/${kind}_map"
    (( count > 0 )) || { echo "no subordinate $kind range in container namespace" >&2; exit 1; }
    printf '%s:%s:%s\n' "$ACCOUNT" "$start" "$count" > "/etc/sub${kind}"
done
mkdir -p /tmp/home /home/linuxbrew/.linuxbrew
chown "$UID_NUMBER:$GID_NUMBER" /tmp/home /home/linuxbrew/.linuxbrew
usermod --home /tmp/home "$ACCOUNT"
systemctl start "user@$UID_NUMBER.service" || {
    journalctl -b -u "user@$UID_NUMBER.service" --no-pager >&2
    exit 1
}
# The target is this container's private manager, never the host's /run/user.
# The compositor wrapper deliberately refuses literal /run/user runtime paths.
ln -s "/run/user/$UID_NUMBER" /tmp/chairlift-docker-runtime
setpriv --reuid="$UID_NUMBER" --regid="$GID_NUMBER" --clear-groups \
    env HOME=/tmp/home XDG_RUNTIME_DIR=/tmp/chairlift-docker-runtime \
    systemctl --user show-environment
setpriv --reuid="$UID_NUMBER" --regid="$GID_NUMBER" --clear-groups \
    unshare --map-auto --setuid=0 --setgid=0 true
