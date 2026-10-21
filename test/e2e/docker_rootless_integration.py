"""Live Docker installation, socket and UI proof in the dedicated Dakota fixture.

No fake brew, systemctl, installer or socket. The enclosing rootless container
owns the Homebrew prefix, native user manager, HOME and every runtime socket.
"""

import http.client
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / "features" / "lib"))
os.environ.pop("NO_AT_BRIDGE", None)

import chairlift_atspi as atspi  # noqa: E402
import wayland_remote  # noqa: E402

PACKAGES = {"docker", "docker-engine", "rootlesskit", "slirp4netns", "docker-compose", "lazydocker", "dive"}
ENGINE_DEPENDENCIES = {"containerd", "runc", "nftables", "tini"}
PREFIX = Path("/home/linuxbrew/.linuxbrew")
BREW = PREFIX / "bin" / "brew"
READY = "Rootless Docker is ready for this account."
INSTALLABLE = "Installs Docker through Homebrew and runs it for this account without administrator access."


def run(*args, check=True):
    result = subprocess.run(args, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
    print(f"$ {' '.join(map(str, args))}\n{result.stdout}", flush=True)
    if check and result.returncode:
        raise RuntimeError(f"command failed ({result.returncode}): {args}:\n{result.stdout}")
    return result


def formulas():
    return set(run(str(BREW), "list", "--formula").stdout.splitlines())


class DockerHTTP(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.settimeout(5)
        self.sock.connect(str(Path(os.environ["XDG_RUNTIME_DIR"]) / "docker.sock"))


def docker_get(path):
    connection = DockerHTTP("docker", timeout=5)
    try:
        connection.request("GET", path)
        response = connection.getresponse()
        body = response.read()
        assert response.status == 200, f"Docker {path}: HTTP {response.status}: {body!r}"
        return body
    finally:
        connection.close()


def main(binary, output):
    output = Path(output)
    os.environ.update(HOMEBREW_NO_AUTO_UPDATE="1", HOMEBREW_NO_ANALYTICS="1", HOMEBREW_NO_INSTALL_CLEANUP="1")
    run("systemctl", "--user", "show-environment")
    run("unshare", "--map-auto", "--setuid=0", "--setgid=0", "true")
    # Bootstrap the real package manager only; the UI must install every package.
    if not (PREFIX / "Homebrew" / "bin" / "brew").is_file():
        run("git", "clone", "--depth", "1", "https://github.com/Homebrew/brew", str(PREFIX / "Homebrew"))
    BREW.parent.mkdir(exist_ok=True)
    if not BREW.exists():
        BREW.symlink_to("../Homebrew/bin/brew")
    assert not (formulas() & PACKAGES), "Docker fixture is not a clean Homebrew installation"
    unit = Path(os.environ["HOME"]) / ".config" / "systemd" / "user" / "docker.service"
    assert not unit.exists(), "Docker fixture already has a user service"
    assert not (Path(os.environ["XDG_RUNTIME_DIR"]) / "docker.sock").exists(), "Docker fixture already has a socket"

    staged = output / "chairlift"
    shutil.copy2(binary, staged)
    journal = output / "journal.jsonl"
    journal.write_text("")
    env = dict(os.environ)
    env.update(
        CHAIRLIFT_ACTION_JOURNAL=str(journal),
        XDG_CONFIG_HOME=str(Path(env["HOME"]) / ".config"),
        XDG_DATA_HOME=str(Path(env["HOME"]) / ".local" / "share"),
        XDG_CACHE_HOME=str(Path(env["HOME"]) / ".cache"),
    )
    if env.get("CHAIRLIFT_SCHEMA_DIR"):
        env["GSETTINGS_SCHEMA_DIR"] = env["CHAIRLIFT_SCHEMA_DIR"]
    # Deliberately keep native PATH: production must add both brew bin and sbin.
    log_path = output / "chairlift.log"
    with log_path.open("wb") as log:
        process = subprocess.Popen([str(staged)], cwd=output, env=env, stdout=log, stderr=subprocess.STDOUT)
    app = None
    try:
        def started():
            assert process.poll() is None, f"ChairLift exited:\n{log_path.read_text()}"
            return "app: window presented" in log_path.read_text()
        assert atspi.poll(started, timeout=60), "ChairLift never presented its window"
        assert "Running in dry-run mode" not in log_path.read_text(), "live integration accidentally ran in preview"
        assert f"Loaded config from {(output / 'config.dev.yml').resolve()}" in log_path.read_text()
        app = atspi.find_application(timeout=60)
        rows = atspi.poll(lambda: atspi.sidebar_rows(app))
        index = next(i for i, row in enumerate(rows, 1) if atspi.label_text(row) == "Features")
        atspi.press(f"<Alt>{index}")

        def docker_row():
            return atspi.row_containing(atspi.page_root(app), "Enable Docker", timeout=1)

        def toggle():
            return atspi.find(docker_row(), lambda n: atspi.role(n) == "switch", "Docker switch", timeout=1)

        assert atspi.poll(lambda: atspi.sensitive(toggle()) and not atspi.checked(toggle()) and INSTALLABLE in atspi.all_text_under(docker_row())), "uninstalled Docker was not offered"
        atspi.activate(toggle())
        assert atspi.poll(lambda: not atspi.sensitive(toggle()), timeout=15), "Docker install never entered its busy state"
        wayland_remote.screenshot(str(output / "docker-installing.png"))

        def ready():
            log = log_path.read_text()
            assert "views: Enable Docker failed:" not in log, f"live Docker installation failed:\n{log}"
            return atspi.checked(toggle()) and atspi.sensitive(toggle()) and READY in atspi.all_text_under(docker_row())
        assert atspi.poll(ready, timeout=1200, interval=0.5), "Docker UI never reported actual readiness"
        assert PACKAGES | ENGINE_DEPENDENCIES <= formulas(), "UI did not install the complete Docker toolchain and engine dependencies"
        run("systemctl", "--user", "is-active", "docker.service")
        assert docker_get("/_ping").strip() == b"OK", "real daemon did not answer OK"
        info = json.loads(docker_get("/info"))
        assert any(option.startswith("name=rootless") for option in info["SecurityOptions"]), info
        assert Path(info["DockerRootDir"]).is_relative_to(Path(env["HOME"])), info
        assert (Path(env["XDG_RUNTIME_DIR"]) / "docker.sock").stat().st_uid == os.getuid()
        (output / "docker-info.json").write_text(json.dumps(info, indent=2))
        wayland_remote.screenshot(str(output / "docker-ready.png"))
        assert not journal.read_text().strip(), f"unprivileged Docker invoked a helper: {journal.read_text()}"

        installed = formulas()
        atspi.activate(toggle())
        assert atspi.poll(lambda: not atspi.checked(toggle()) and atspi.sensitive(toggle()) and INSTALLABLE in atspi.all_text_under(docker_row()), timeout=60), "Docker UI did not observe disable"
        state = run("systemctl", "--user", "show", "docker.service", "--property=LoadState,ActiveState").stdout
        assert "LoadState=loaded" in state and "ActiveState=inactive" in state, state
        try:
            docker_get("/_ping")
        except OSError:
            pass
        else:
            raise AssertionError("Docker socket still responds after disable")
        assert formulas() == installed, "disable uninstalled packages"
        assert unit.is_file(), "disable deleted the user service"
        assert Path(info["DockerRootDir"]).is_dir(), "disable deleted Docker data"
        assert not journal.read_text().strip(), "Docker disable invoked a privileged helper"
        wayland_remote.screenshot(str(output / "docker-disabled.png"))
        print("LIVE DOCKER PROOF: UI install -> Homebrew engine/dependencies -> rootless HTTP200 OK -> ready UI -> retained-install disable", flush=True)
    except BaseException:
        if app is not None:
            with (output / "tree.txt").open("w") as dump:
                atspi.dump(atspi.main_window(app), dump)
            try:
                wayland_remote.screenshot(str(output / "failure.png"))
            except Exception as error:
                print(f"failure screenshot unavailable: {error}", flush=True)
        raise
    finally:
        run("systemctl", "--user", "disable", "--now", "docker.service", check=False)
        run("journalctl", "--user", "--unit", "docker.service", "--no-pager", check=False)
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
        try:
            process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()


if __name__ == "__main__":
    main(*sys.argv[1:])
