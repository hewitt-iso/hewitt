"""Regression checks for agent window branches without Docker or agent accounts."""
from pathlib import Path
import subprocess
import tempfile

source = Path(__file__).with_name('hewitt-agent').read_text()
function = source[source.index('create_worktree_if_needed() {'):source.index('\nexec_agent() {')]
with tempfile.TemporaryDirectory() as temporary:
    root = Path(temporary)
    repo = root / 'project'
    subprocess.run(['git', 'init', '-q', str(repo)], check=True)
    subprocess.run(['git', '-C', str(repo), '-c', 'user.name=Test', '-c', 'user.email=test@example.com', 'commit', '--allow-empty', '-qm', 'base'], check=True)
    script = function + '''
set -eu
export HEWITT_WORKSPACE_MODE=worktree
export HEWITT_REPO="$1"
export HEWITT_WORKTREES_DIR="$2"
choice=codex
create_worktree_if_needed
 test "$(git branch --show-current)" = codex-1
create_worktree_if_needed
 test "$(git branch --show-current)" = codex-2
choice=claude
create_worktree_if_needed
 test "$(git branch --show-current)" = claude-1
'''
    subprocess.run(['bash', '-c', script, 'test', str(repo), str(root)], check=True)
