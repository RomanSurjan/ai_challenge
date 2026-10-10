#!/usr/bin/env python3
"""Export only the PUBLIC certificate for verified curl/LAN browser trust."""
from pathlib import Path
import shutil
import subprocess

root=Path(__file__).resolve().parents[1]
source=root/'.runtime/data/caddy/pki/authorities/local/root.crt'
if not source.exists(): raise SystemExit('Start Caddy first; public CA not created yet')
shutil.copy2(source,root/'.runtime/root.crt')
(root/'artifacts').mkdir(exist_ok=True)
shutil.copy2(source,root/'artifacts/lan-root.crt')
print(subprocess.check_output(['openssl','x509','-in',str(source),'-noout','-fingerprint','-sha256'],text=True).strip())
