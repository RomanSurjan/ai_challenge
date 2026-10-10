#!/usr/bin/env python3
"""Install/control user launchd agents. Ollama remains the existing native service."""
import argparse
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
RUNTIME = ROOT/'.runtime'
LABELS = {'backend':'local.day30.backend', 'proxy':'local.day30.proxy', 'awake':'local.day30.awake'}
DOMAIN = f'gui/{os.getuid()}'
AGENTS = Path.home()/'Library/LaunchAgents'


def run(*args, check=True):
    return subprocess.run(args, check=check, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)


def definitions():
    environment = {'XDG_DATA_HOME':str(RUNTIME/'data'), 'XDG_CONFIG_HOME':str(RUNTIME/'caddy-config')}
    return {
        'backend': [sys.executable, str(ROOT/'web_server.py'), '--config', str(RUNTIME/'config.json')],
        'proxy': [str(RUNTIME/'caddy'), 'run', '--config', str(RUNTIME/'Caddyfile'), '--adapter', 'caddyfile'],
        # Prevent idle sleep while plugged into AC. Closing lid still interrupts availability.
        'awake': ['/usr/bin/caffeinate', '-s'],
    }, environment


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('action', choices=['install','start','stop','restart','status','uninstall'])
    parser.add_argument('--component', choices=[*LABELS,'all'], default='all')
    args = parser.parse_args()
    os.umask(0o077)
    keys = list(LABELS) if args.component == 'all' else [args.component]
    if args.action == 'install':
        if not (RUNTIME/'config.json').exists():
            raise SystemExit('Run setup.py first')
        AGENTS.mkdir(exist_ok=True)
        commands, env = definitions()
        for key in keys:
            label = LABELS[key]
            plist = {'Label':label, 'ProgramArguments':commands[key], 'WorkingDirectory':str(ROOT),
                     'RunAtLoad':True, 'KeepAlive':True, 'ThrottleInterval':2,
                     'EnvironmentVariables':env, 'StandardOutPath':str(RUNTIME/f'{key}.stdout.log'),
                     'StandardErrorPath':str(RUNTIME/f'{key}.stderr.log')}
            target = AGENTS/f'{label}.plist'
            with target.open('wb') as f:
                plistlib.dump(plist, f)
            target.chmod(0o600)
            # Existing loaded agents must be stopped explicitly, never silently replaced.
            result = run('launchctl','bootstrap',DOMAIN,str(target),check=False)
            if result.returncode:
                raise SystemExit(f'bootstrap {key} failed; use status/restart (code {result.returncode})')
        print('Installed launchd agents:', ', '.join(keys))
    elif args.action in ('start','stop','restart','uninstall'):
        for key in keys:
            label = LABELS[key]
            target = AGENTS/f'{label}.plist'
            if args.action == 'restart':
                run('launchctl','kickstart','-k',f'{DOMAIN}/{label}')
            elif args.action == 'start':
                run('launchctl','bootstrap',DOMAIN,str(target))
            else:
                run('launchctl','bootout',f'{DOMAIN}/{label}',check=False)
                if args.action == 'uninstall' and target.exists():
                    target.unlink()
        print(args.action, ', '.join(keys))
    else:
        status = {}
        for key,label in LABELS.items():
            result = run('launchctl','list',label,check=False)
            status[key] = {'loaded':result.returncode == 0}
            if result.returncode == 0:
                try:
                    # launchctl list output isn't JSON; extract only PID/status, never environment.
                    import re
                    for name in ['PID','LastExitStatus']:
                        match = re.search(r'"'+name+r'" = (\d+)',result.stdout)
                        if match: status[key][name] = int(match[1])
                except ValueError:
                    pass
        print(json.dumps(status,indent=2))


if __name__ == '__main__':
    main()
