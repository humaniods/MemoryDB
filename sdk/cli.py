#!/usr/bin/env python3
"""mdb — command-line access to a MemoryDB server for scripts, cron jobs and CI.

Connection comes from --url or MEMORYDB_URL (accepts http://host:port plus
MEMORYDB_TOKEN, or a memorydb://TOKEN@host:port connection string).
`mdb serve` installs and starts the server itself, so pip is the only thing you need.
Every command prints the JSON response on stdout; errors exit non-zero.
"""
import argparse
import hashlib
import json
import os
import platform
import secrets
import shutil
import subprocess
import sys
import tarfile
import urllib.error
import urllib.request
from urllib.parse import urlparse

sys.path.insert(0, str(os.path.dirname(os.path.abspath(__file__))))
from memorydb import MemoryDB, MemoryDBError  # noqa: E402

SERVER_VERSION = "0.2.0"          # the release tag mdb serve downloads by default
REPO = "humaniods/MemoryDB"


def load_db(args):
    url = args.url or os.environ.get("MEMORYDB_URL", "http://127.0.0.1:8080")
    if url.startswith("memorydb"):
        return MemoryDB.from_url(url)
    token = args.token or os.environ.get("MEMORYDB_TOKEN")
    if not token:
        sys.exit("no token: pass --token or set MEMORYDB_TOKEN/MEMORYDB_URL")
    return MemoryDB(url, token=token)


def read_json_arg(value, path):
    if path:
        with open(path) as fh:
            return json.load(fh)
    if value:
        return json.loads(value)
    sys.exit("provide --json or --file")


def emit(obj):
    print(json.dumps(obj, indent=2, default=str))


# -- mdb serve: the database itself, installed over pip, with no Go or git -----------------
def home():
    path = os.environ.get("MEMORYDB_HOME") or os.path.join(os.path.expanduser("~"), ".memorydb")
    os.makedirs(path, exist_ok=True)
    return path


def platform_target():
    system = platform.system()
    if system == "Windows" or system.startswith(("MINGW", "MSYS", "CYGWIN")):
        sys.exit("mdb serve: no native Windows build (the database lock is Unix-only); "
                 "run the server in WSL, Docker or on your app host.")
    osname = {"Linux": "linux", "Darwin": "darwin"}.get(system)
    if osname is None:
        sys.exit(f"mdb serve: unsupported operating system: {system}")
    arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(
        platform.machine().lower())
    if arch is None:
        sys.exit(f"mdb serve: unsupported CPU architecture: {platform.machine()}")
    return osname, arch


def fetch(url, dest):
    """Copy a release archive down over https, or from a local path/file:// URL."""
    if url.startswith("https://"):
        with urllib.request.urlopen(url, timeout=120) as response, open(dest, "wb") as out:
            shutil.copyfileobj(response, out)
        return
    source = urlparse(url).path if url.startswith("file://") else url
    if not os.path.exists(source):
        raise FileNotFoundError(source)
    shutil.copyfile(source, dest)


def checksum(path):
    digest = hashlib.sha256()
    with open(path, "rb") as fh:
        for block in iter(lambda: fh.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def extract_binary(archive, dest):
    """Unpack only the memorydb member, so a crafted path in a tar entry cannot write elsewhere."""
    with tarfile.open(archive) as tar:
        member = next((m for m in tar.getmembers()
                       if m.isfile() and os.path.basename(m.name) == "memorydb"), None)
        if member is None:
            sys.exit(f"mdb serve: {os.path.basename(archive)} holds no memorydb binary")
        with tar.extractfile(member) as src, open(dest, "wb") as out:
            shutil.copyfileobj(src, out)
    os.chmod(dest, 0o755)
    return dest


def check_against_sidecar(archive, sidecar):
    """A checksums file that exists but does not name this archive is treated as a mismatch."""
    if not sidecar or not os.path.exists(sidecar):
        return
    wanted = {}
    with open(sidecar) as fh:
        for line in fh:
            parts = line.split(None, 1)
            if len(parts) == 2:
                wanted[parts[1].strip().lstrip("*")] = parts[0]
    name = os.path.basename(archive)
    if name not in wanted:
        sys.exit(f"mdb serve: {name} is not listed in {sidecar}; refusing to install it")
    if checksum(archive) != wanted[name]:
        sys.exit(f"mdb serve: sha256 mismatch for {name}; refusing to install it")


def install_from_archive(archive, version):
    check_against_sidecar(archive, os.path.join(os.path.dirname(archive), "sha256sums.txt"))
    staged = os.path.join(home(), "bin", f"memorydb-{version}.tmp")
    target = binary_path(version)
    extract_binary(archive, staged)
    os.replace(staged, target)
    return target


def binary_path(version):
    return os.path.join(home(), "bin", f"memorydb-{version}")


def ensure_binary(args):
    if args.bin:
        return os.path.abspath(args.bin)
    target = binary_path(args.version)
    if os.path.exists(target) and not args.force:
        return target
    os.makedirs(os.path.join(home(), "bin"), exist_ok=True)
    if args.from_archive:
        return install_from_archive(args.from_archive, args.version)
    if args.source:
        if shutil.which("go") is None:
            sys.exit("mdb serve: --source needs Go on PATH")
        built = os.path.join(home(), "bin", f"memorydb-{args.version}.tmp")
        subprocess.run(["go", "build", "-trimpath", "-o", built, "."], cwd=args.source, check=True)
        os.replace(built, target)
        return target
    osname, arch = platform_target()
    name = f"memorydb-{args.version}-{osname}-{arch}.tar.gz"
    base = args.dist_base.format(repo=REPO, version=args.version)
    archive = os.path.join(home(), "bin", name)
    print(f"downloading {base}/{name}")
    try:
        fetch(base + "/" + name, archive)
        try:
            fetch(base + "/sha256sums.txt", os.path.join(os.path.dirname(archive), "sha256sums.txt"))
        except FileNotFoundError:
            sys.exit("mdb serve: this release publishes no sha256sums.txt; refusing to run an "
                     "unverified binary. Use --from <archive>, --source <tree> or --bin <path>.")
    except (OSError, urllib.error.URLError) as exc:
        sys.exit(f"mdb serve: could not fetch a release binary for {osname}/{arch} "
                 f"({exc.__class__.__name__}). If this version is not released yet, use "
                 f"--source <path-to-checkout> or --bin <path-to-memorydb>.")
    return install_from_archive(archive, args.version)


def serve_token(explicit):
    if explicit:
        return explicit
    if os.environ.get("MEMORYDB_TOKEN"):
        return os.environ["MEMORYDB_TOKEN"]
    path = os.path.join(home(), "token")
    if os.path.exists(path):
        with open(path) as fh:
            token = fh.read().strip()
        if token:
            return token
    token = secrets.token_urlsafe(32)
    handle = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(handle, "w") as fh:
        fh.write(token + "\n")
    return token


def serve(args):
    binary = ensure_binary(args)
    data = args.data or os.path.join(home(), "data", "memory.wal")
    os.makedirs(os.path.dirname(os.path.abspath(data)), exist_ok=True)
    token = serve_token(args.token)
    env = dict(os.environ, MEMORYDB_TOKEN=token)
    command = [binary, "-addr", args.addr, "-data", data]
    if args.worker:
        command.append("-worker")
    command += args.extra
    scheme = "memorydbs" if args.extra and "-tls-cert" in args.extra else "memorydb"
    host = args.addr if "://" not in args.addr else urlparse(args.addr).netloc
    print(f"starting MemoryDB on {host}")
    print(f"  data:    {data}")
    print(f"  connect: {scheme}://{token}@{host}")
    print(f"  console: http{'s' if scheme == 'memorydbs' else ''}://{host}  "
          f"studio: http{'s' if scheme == 'memorydbs' else ''}://{host}/studio/")
    sys.stdout.flush()
    return subprocess.run(command, env=env).returncode


def main(argv=None):
    parser = argparse.ArgumentParser(prog="mdb", description=__doc__.splitlines()[0])
    parser.add_argument("--url", help="http://host:port or memorydb://TOKEN@host:port")
    parser.add_argument("--token")
    sub = parser.add_subparsers(dest="cmd", required=True)

    sub.add_parser("health")

    p = sub.add_parser("collections"); p.add_argument("action", choices=["ls", "create"])
    p.add_argument("name", nargs="?"); p.add_argument("--json", help="schema JSON")
    p.add_argument("--file")

    p = sub.add_parser("doc"); p.add_argument("action", choices=["add", "get", "rm", "patch"])
    p.add_argument("collection"); p.add_argument("id", nargs="?")
    p.add_argument("--json"); p.add_argument("--file"); p.add_argument("--version", type=int)

    p = sub.add_parser("query"); p.add_argument("collection")
    p.add_argument("--where", default="[]"); p.add_argument("--order"); p.add_argument("--desc", action="store_true")
    p.add_argument("--limit", type=int, default=50); p.add_argument("--fields", help="comma-separated projection")

    p = sub.add_parser("memory"); p.add_argument("action", choices=["add", "ls", "history"])
    p.add_argument("--kind", default="fact"); p.add_argument("--content"); p.add_argument("--id")
    p.add_argument("--source", default="cli"); p.add_argument("--q", default=""); p.add_argument("--limit", type=int, default=50)

    p = sub.add_parser("search"); p.add_argument("text")
    p.add_argument("--kind", default=""); p.add_argument("--limit", type=int, default=10)

    p = sub.add_parser("ingest"); p.add_argument("--file", required=True)
    p = sub.add_parser("tx"); p.add_argument("--file", required=True); p.add_argument("--key")

    p = sub.add_parser("serve", help="install (if needed) and run the MemoryDB server")
    p.add_argument("--addr", default="127.0.0.1:8080")
    p.add_argument("--data", help="journal path (default ~/.memorydb/data/memory.wal)")
    p.add_argument("--token", default=argparse.SUPPRESS, help="admin token (default MEMORYDB_TOKEN or a generated one)")
    p.add_argument("--version", default=SERVER_VERSION, help="release binary to fetch")
    p.add_argument("--bin", help="run this already-built server binary")
    p.add_argument("--from", dest="from_archive", help="install the server from this release archive")
    p.add_argument("--source", help="build the server from this source tree (needs Go)")
    p.add_argument("--dist-base", default="https://github.com/{repo}/releases/download/v{version}",
                   help="release download base; {repo} and {version} are substituted")
    p.add_argument("--worker", action="store_true", help="also run durable ingestion jobs")
    p.add_argument("--force", action="store_true", help="re-download even when the binary is cached")
    p.add_argument("extra", nargs=argparse.REMAINDER,
                   help="more server flags, e.g. -- -tls-cert cert.pem -tls-key key.pem")

    args = parser.parse_args(argv)
    if args.cmd == "serve":
        return serve(args)
    db = load_db(args)
    try:
        if args.cmd == "health":
            emit(db.request("GET", "health/"))
        elif args.cmd == "collections":
            if args.action == "ls":
                emit(db.collections.all())
            else:
                schema = read_json_arg(args.json, args.file) if (args.json or args.file) else {}
                emit(db.collections.create(args.name, **schema))
        elif args.cmd == "doc":
            if args.action == "add":
                emit(db.collection(args.collection).objects.create(data=read_json_arg(args.json, args.file)))
            elif args.action == "get":
                emit(db.collection(args.collection).objects.get(pk=args.id))
            elif args.action == "patch":
                emit(db.collection(args.collection).objects.update(pk=args.id, version=args.version, **read_json_arg(args.json, args.file)))
            else:
                emit(db.collection(args.collection).objects.delete(pk=args.id, version=args.version))
        elif args.cmd == "query":
            q = {"where": json.loads(args.where), "limit": args.limit}
            if args.order:
                q["order_by"] = args.order
                q["descending"] = args.desc
            if args.fields:
                q["projection"] = args.fields.split(",")
            emit(db.request("POST", "query/" + args.collection + "/", q))
        elif args.cmd == "memory":
            if args.action == "add":
                emit(db.objects.create(kind=args.kind, content=args.content, source=args.source))
            elif args.action == "ls":
                emit(db.objects.filter(kind=args.kind if args.kind != "fact" else "", source=args.source, q=args.q, limit=args.limit))
            else:
                emit(db.objects.history(pk=args.id)["results"])
        elif args.cmd == "search":
            emit(db.objects.search(args.text, kind=args.kind, limit=args.limit))
        elif args.cmd == "ingest":
            with open(args.file) as fh:
                payload = json.load(fh)
            emit(db.ingest(payload["messages"], facts=payload.get("facts"), source=payload.get("source", "cli")))
        elif args.cmd == "tx":
            with open(args.file) as fh:
                payload = json.load(fh)
            emit(db.transaction(payload["changes"], idempotency_key=args.key))
    except MemoryDBError as exc:
        sys.exit(f"mdb error [{exc.status}]: {exc}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
