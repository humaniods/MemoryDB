#!/usr/bin/env python3
"""Familiar command entry point; the database server itself is Go."""
import os
import shutil
import subprocess
import sys
from pathlib import Path

root = Path(__file__).resolve().parent
command = sys.argv[1] if len(sys.argv) > 1 else "help"
go = shutil.which("go")
if command == "help":
    print("python manage.py [runserver | test | check | build]")
    raise SystemExit(0)
if not go:
    raise SystemExit("Install Go 1.24 or newer and put go on PATH.")
commands = {
    "runserver": [go, "run", "-buildvcs=false", ".", *sys.argv[2:]],
    "test": [go, "test", "-race", "./..."],
    "check": [go, "vet", "./..."],
    "build": [go, "build", "-buildvcs=false", "-o", "memorydb", "."],
}
if command not in commands:
    raise SystemExit("Unknown command. Run python manage.py help")
if command == "runserver" and "-auth" not in sys.argv and len(os.environ.get("MEMORYDB_TOKEN", "")) < 16:
    raise SystemExit("Set MEMORYDB_TOKEN to a random token of at least 16 characters.")
raise SystemExit(subprocess.call(commands[command], cwd=root))
