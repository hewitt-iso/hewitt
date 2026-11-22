"""Checks that hewitt-agent runs the launch Hewitt describes, without Docker."""
from pathlib import Path
import base64
import subprocess

source = Path(__file__).with_name('hewitt-agent').read_text()
functions = source[source.index('decode_args() {'):source.index('\npublish() {')]
functions += source[source.index('\nlaunch_args() {'):source.index('\nexec_launch() {')]


def encode(args):
    return base64.b64encode(b''.join(a.encode() + b'\0' for a in args)).decode()


mark = '{workdir:abc}'
prompt = 'fix it\nthen say "{workdir:other}" and $HOME'
env = {
    'PATH': '/usr/bin:/bin',
    'HEWITT_LAUNCH': encode(['env', '-u', 'KEY', 'codex', f'projects."{mark}".trust', '--', prompt]),
    'HEWITT_WORKDIR_MARK': mark,
    'HEWITT_LAUNCH_PI': encode(['env', 'pi', '']),
}
script = functions + '''
set -euo pipefail
cd /tmp
launch_args
printf '%s\\0' "${ARGS[@]}"
'''
out = subprocess.run(['bash', '-c', script], env=env, check=True, capture_output=True).stdout
assert out.split(b'\0')[:-1] == [b'env', b'-u', b'KEY', b'codex', b'projects."/tmp".trust', b'--', prompt.encode()], out

# The menu's launch of one agent, including an empty argument.
script = functions + '''
set -euo pipefail
launch_args pi
printf '%s\\0' "${ARGS[@]}"
test -z "${HEWITT_LAUNCH_PI:-}" && test -z "${HEWITT_LAUNCH:-}"
'''
out = subprocess.run(['bash', '-c', script], env=env, check=True, capture_output=True).stdout
assert out.split(b'\0')[:-1] == [b'env', b'pi', b''], out

# A missing launch fails instead of running something else.
result = subprocess.run(['bash', '-c', functions + '\nlaunch_args codex\n'], env={'PATH': '/usr/bin:/bin'}, capture_output=True)
assert result.returncode == 2 and b'no launch for codex' in result.stderr, result
