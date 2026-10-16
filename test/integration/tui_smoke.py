"""Read-only POSIX PTY smoke test. Requires a reachable Docker daemon and bin/dodu."""
import fcntl
import os
import pty
import select
import struct
import subprocess
import termios
import time

master, slave = pty.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 32, 120, 0, 0))
process = subprocess.Popen(
    [os.environ.get("DODU_BINARY", "./bin/dodu"), "--readonly"],
    stdin=slave, stdout=slave, stderr=slave,
    env=dict(os.environ, TERM="xterm-256color"), start_new_session=True,
)
os.close(slave)
output = bytearray()


def read_for(seconds):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        if select.select([master], [], [], 0.1)[0]:
            try:
                output.extend(os.read(master, 65536))
            except OSError:
                break


try:
    deadline = time.monotonic() + 35
    while b"by-type" not in output and time.monotonic() < deadline:
        read_for(0.2)
    assert b"by-type" in output, "atlas did not load"
    # Separate events model real keystrokes, not a single multi-rune paste.
    for key in [b"l", b"d", b"p", b"\x1b", b"?", b"?", b"q"]:
        os.write(master, key)
        read_for(0.3)
    process.wait(timeout=5)
    for text in [b"Details", b"Cleanup preview", b"Read-only", b"keys"]:
        assert text in output, f"missing terminal panel {text!r}"
    assert process.returncode == 0, process.returncode
    print("PASS: actual PTY atlas, details, mark/preview, readonly, help, quit")
finally:
    if process.poll() is None:
        process.terminate()
        process.wait(timeout=5)
    os.close(master)
