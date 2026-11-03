"""Build-time regression: a tmux pane must receive the outer terminal palette."""

import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import shlex
import struct
import subprocess
import sys
import tempfile
import termios
import time
import tty


def probe(destination):
    tty.setraw(0)
    # Allow the simulated terminal to finish answering tmux's startup queries.
    time.sleep(0.5)
    os.write(1, b"\x1b]10;?\x07\x1b]11;?\x07")
    received = b""
    deadline = time.monotonic() + 2
    while time.monotonic() < deadline:
        if select.select([0], [], [], 0.05)[0]:
            received += os.read(0, 4096)
        if b"]10;rgb:" in received and b"]11;rgb:" in received:
            break
    Path(destination).write_text(json.dumps(received.decode("ascii", "replace")))
    # Keep the server alive until the harness has collected the result.
    time.sleep(5)


def check():
    with tempfile.TemporaryDirectory(prefix="hewitt-palette-") as directory:
        socket = directory + "/tmux.sock"
        result = Path(directory) / "result.json"
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
        command = shlex.join([sys.executable, __file__, "--probe", str(result)])
        client = subprocess.Popen(
            ["tmux", "-f", "/dev/null", "-S", socket, "new-session", command],
            stdin=slave, stdout=slave, stderr=slave,
            env={**{k: v for k, v in os.environ.items() if k not in {"TMUX", "TMUX_PANE"}},
                 "TERM": "xterm-256color"},
        )
        os.close(slave)
        pending = b""
        query = re.compile(rb"\x1b\](10|11);\?(?:\x07|\x1b\\)")
        try:
            deadline = time.monotonic() + 8
            while not result.exists() and time.monotonic() < deadline:
                if select.select([master], [], [], 0.05)[0]:
                    try:
                        pending += os.read(master, 65536)
                    except OSError:
                        break
                    for match in query.finditer(pending):
                        slot = match.group(1)
                        color = b"eeee/eeee/eeee" if slot == b"10" else b"2323/2626/2727"
                        os.write(master, b"\x1b]" + slot + b";rgb:" + color + b"\x07")
                    pending = query.sub(b"", pending)[-4096:]
            if not result.exists():
                raise RuntimeError("tmux palette probe did not finish")
            received = json.loads(result.read_text())
            for expected in ["]10;rgb:eeee/eeee/eeee", "]11;rgb:2323/2626/2727"]:
                if expected not in received:
                    raise RuntimeError(f"missing terminal palette reply {expected!r}: {received!r}")
            print("tmux forwards foreground and background colors to panes")
        finally:
            subprocess.run(["tmux", "-S", socket, "kill-server"], capture_output=True)
            if client.poll() is None:
                client.terminate()
            client.wait(timeout=5)
            os.close(master)


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--probe":
        probe(sys.argv[2])
    else:
        check()
