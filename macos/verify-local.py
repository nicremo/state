#!/usr/bin/env python3
"""Exercise a fresh local server and the shared Swift certificate verifier."""
from __future__ import annotations

import json
import os
from pathlib import Path
import queue
import subprocess
import tempfile
import threading
import urllib.parse


def main() -> None:
    root = Path(__file__).resolve().parent.parent
    binary = root / "dist/State Server.app/Contents/Resources/state-server"
    with tempfile.TemporaryDirectory(prefix="state-desktop-check-") as temporary:
        process = subprocess.Popen(
            [str(binary), "desktop", "--data", temporary, "--host", "test.local",
             "--https", "127.0.0.1:0", "--local-http", "127.0.0.1:0"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            text=True,
        )
        messages: queue.Queue[dict[str, object]] = queue.Queue()

        def consume() -> None:
            assert process.stdout is not None
            for line in process.stdout:
                messages.put(json.loads(line))

        reader = threading.Thread(target=consume, daemon=True)
        reader.start()
        try:
            status = messages.get(timeout=20)
            endpoint = urllib.parse.urlsplit(str(status["server_url"]))
            environment = os.environ.copy()
            environment["STATE_TEST_ORIGIN"] = f"https://127.0.0.1:{endpoint.port}"
            environment["STATE_TEST_PIN"] = str(status["fingerprint"])
            subprocess.run(
                ["swift", "test", "--filter", "liveServerTrustMatchesOnlyScannedCertificate"],
                cwd=root, env=environment, check=True, timeout=120,
            )
            print("PASS: real TLS connection, wrong-certificate rejection, wrong-origin rejection")
        finally:
            assert process.stdin is not None
            process.stdin.close()
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.terminate()
                process.wait(timeout=10)
            reader.join(timeout=5)
        if process.returncode != 0:
            raise RuntimeError("Desktop server did not shut down cleanly")
        print("PASS: clean shutdown after parent pipe closes")


if __name__ == "__main__":
    main()
