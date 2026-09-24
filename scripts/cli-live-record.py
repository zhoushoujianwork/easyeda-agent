#!/usr/bin/env python3
"""Run one CLI test command and preserve its raw result without overwriting evidence."""

import argparse
import datetime as dt
import json
import pathlib
import subprocess
import sys
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", required=True, type=pathlib.Path, help="new evidence JSON path")
    parser.add_argument("--timeout", type=int, default=180, help="command timeout in seconds")
    parser.add_argument("command", nargs=argparse.REMAINDER, help="command after --")
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        parser.error("provide a command after --")
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    if args.out.exists():
        parser.error("--out already exists; evidence is immutable")
    args.out.parent.mkdir(parents=True, exist_ok=True)

    started_at = dt.datetime.now(dt.timezone.utc).isoformat()
    start = time.monotonic()
    try:
        result = subprocess.run(command, capture_output=True, text=True,
                                timeout=args.timeout, check=False)
        exit_code, stdout, stderr, error = result.returncode, result.stdout, result.stderr, None
    except subprocess.TimeoutExpired as exc:
        exit_code = 124
        stdout = exc.stdout.decode(errors="replace") if isinstance(exc.stdout, bytes) else (exc.stdout or "")
        stderr = exc.stderr.decode(errors="replace") if isinstance(exc.stderr, bytes) else (exc.stderr or "")
        error = str(exc)
    except OSError as exc:
        exit_code, stdout, stderr, error = 127, "", "", str(exc)

    evidence = {
        "command": command,
        "startedAt": started_at,
        "completedAt": dt.datetime.now(dt.timezone.utc).isoformat(),
        "durationSeconds": round(time.monotonic() - start, 3),
        "exitCode": exit_code,
        "stdout": stdout,
        "stderr": stderr,
    }
    if error is not None:
        evidence["error"] = error
    with args.out.open("x", encoding="utf-8") as handle:
        json.dump(evidence, handle, ensure_ascii=False, indent=2)
        handle.write("\n")
    print(json.dumps({"exitCode": exit_code, "evidence": str(args.out)}, ensure_ascii=False))
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
