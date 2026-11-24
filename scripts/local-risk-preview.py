#!/usr/bin/env python3
"""Isolated local preview: empty database, fresh keys, loopback listeners.
Build first: go build -o /tmp/nofx-risk-local/nofx . && cd web && npm run build
Run: python3 scripts/local-risk-preview.py
"""
import base64
import os
from pathlib import Path
import secrets
import signal
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
runtime = Path(tempfile.mkdtemp(prefix='nofx-risk-preview-'))
os.chmod(runtime, 0o700)
key = subprocess.check_output(['openssl', 'genrsa', '2048'], stderr=subprocess.DEVNULL).decode()
env = os.environ.copy()
# Deliberately do not load the project's .env or reuse trading credentials.
for name in list(env):
    if any(part in name.upper() for part in ('API_KEY', 'SECRET', 'PRIVATE_KEY', 'PASSWORD', 'TOKEN')):
        env.pop(name, None)
env.update(JWT_SECRET=secrets.token_urlsafe(48), RSA_PRIVATE_KEY=key,
           DATA_ENCRYPTION_KEY=base64.b64encode(secrets.token_bytes(32)).decode(),
           DB_TYPE='sqlite', DB_PATH=str(runtime / 'preview.db'), API_SERVER_HOST='127.0.0.1',
           API_SERVER_PORT='18080', EXPERIENCE_IMPROVEMENT='false',
           VITE_API_PROXY_TARGET='http://127.0.0.1:18080')
backend_log = open(runtime / 'backend.log', 'w')
frontend_log = open(runtime / 'frontend.log', 'w')
children = []
def stop(*_):
    for child in children:
        child.terminate()
    for child in children:
        try:
            child.wait(timeout=10)
        except subprocess.TimeoutExpired:
            child.kill()
    raise SystemExit(0)
signal.signal(signal.SIGINT, stop)
signal.signal(signal.SIGTERM, stop)
try:
    children.append(subprocess.Popen(['/tmp/nofx-risk-local/nofx'], cwd=runtime, env=env, stdout=backend_log, stderr=subprocess.STDOUT))
    children.append(subprocess.Popen(['npm', 'run', 'dev', '--', '--host', '127.0.0.1', '--port', '13000', '--strictPort'], cwd=root / 'web', env=env, stdout=frontend_log, stderr=subprocess.STDOUT))
    print(f'Local UI: http://127.0.0.1:13000\nAPI: http://127.0.0.1:18080\nIsolated data/logs: {runtime}', flush=True)
    children[0].wait()
finally:
    stop()
