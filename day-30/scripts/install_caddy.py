#!/usr/bin/env python3
"""Install a pinned official Caddy binary into private runtime after SHA-512 verification."""
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import tarfile
import urllib.request

ROOT=Path(__file__).resolve().parents[1]
VERSION='2.11.7'


def main():
    if platform.system()!='Darwin' or platform.machine()!='arm64':
        raise SystemExit('This deployment installer targets macOS Apple Silicon')
    os.umask(0o077)
    runtime=ROOT/'.runtime'; runtime.mkdir(mode=0o700,exist_ok=True); runtime.chmod(0o700)
    name=f'caddy_{VERSION}_mac_arm64.tar.gz'
    base=f'https://github.com/caddyserver/caddy/releases/download/v{VERSION}/'
    archive=urllib.request.urlopen(base+name,timeout=90).read()
    checks=urllib.request.urlopen(base+f'caddy_{VERSION}_checksums.txt',timeout=30).read().decode()
    expected=next(line.split()[0] for line in checks.splitlines() if line.split()[-1]==name)
    if hashlib.sha512(archive).hexdigest()!=expected:
        raise SystemExit('Official SHA-512 mismatch; binary not installed')
    with tarfile.open(fileobj=io.BytesIO(archive),mode='r:gz') as t:
        data=t.extractfile('caddy').read()
    binary=runtime/'caddy'; binary.write_bytes(data); binary.chmod(0o700)
    (ROOT/'artifacts').mkdir(exist_ok=True)
    (ROOT/'artifacts/caddy-install.json').write_text(json.dumps({'version':VERSION,'asset':name,
        'url':base+name,'verifiedSha512':expected,'sha256':hashlib.sha256(archive).hexdigest()},indent=2)+'\n')
    print('Official Caddy installed; SHA-512 verified')


if __name__=='__main__': main()
