"""Check the generated shell detach command without Docker or tmux."""
from pathlib import Path
import os
import subprocess
import tempfile

source = Path(__file__).with_name('hewitt-agent').read_text()
start = source.index('\ndetach() {')
function = source[start:source.index('\nprintf ', source.index('\n}', start))].replace('\\$', '$')
with tempfile.TemporaryDirectory() as directory:
    fake = Path(directory) / 'tmux'
    fake.write_text('#!/bin/sh\nprintf "%s\\n" "$*"\n')
    fake.chmod(0o755)
    env = dict(os.environ, PATH=directory + ':' + os.environ['PATH'], TMUX='test')
    result = subprocess.run(['bash', '-c', function + '\ndetach'], env=env, text=True, capture_output=True)
    assert result.returncode == 0, result.stderr
    assert result.stdout.strip() == 'detach-client', result.stdout
    env['TMUX'] = ''
    result = subprocess.run(['bash', '-c', function + '\ndetach'], env=env, text=True, capture_output=True)
    assert result.returncode == 1
    assert 'no tmux session' in result.stderr
