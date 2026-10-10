#!/usr/bin/env python3
"""Generate private configuration once; never print credentials."""
import argparse
import json
import os
from pathlib import Path
import secrets
import subprocess

ROOT = Path(__file__).resolve().parents[1]
RUNTIME = ROOT / '.runtime'


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--lan-ip', required=True)
    args = parser.parse_args()
    import ipaddress
    ipaddress.IPv4Address(args.lan_ip)
    os.umask(0o077)
    RUNTIME.mkdir(mode=0o700, exist_ok=True)
    RUNTIME.chmod(0o700)
    config_path = RUNTIME / 'config.json'
    if config_path.exists():
        raise SystemExit('Private config already exists; retained. Update it explicitly if LAN address changes.')
    password = secrets.token_urlsafe(24)
    proxy_secret = secrets.token_hex(32)
    hashed = subprocess.check_output([str(RUNTIME/'caddy'), 'hash-password'],
                                    input=(password+'\n').encode()).decode().strip()
    config = json.loads((ROOT/'config.example.json').read_text())
    config['proxy_secret'] = proxy_secret
    config['allowed_origins'] = [f'https://{args.lan_ip}:8443', 'https://localhost:8443']
    config_path.write_text(json.dumps(config, indent=2)+'\n')
    caddy = (ROOT/'deploy/Caddyfile.example').read_text().replace('192.168.3.53', args.lan_ip)
    caddy = caddy.replace('REPLACE_WITH_BCRYPT_HASH', hashed).replace('REPLACE_WITH_RANDOM_PROXY_SECRET', proxy_secret)
    (RUNTIME/'Caddyfile').write_text(caddy)
    (RUNTIME/'access.txt').write_text(f'URL: https://{args.lan_ip}:8443\nUsername: demo\nPassword: {password}\n')
    (RUNTIME/'curl.conf').write_text(f'user = "demo:{password}"\ncacert = "{RUNTIME}/root.crt"\n')
    print('Private configuration created in .runtime (mode 700/600). Credentials not printed.')


if __name__ == '__main__':
    main()
