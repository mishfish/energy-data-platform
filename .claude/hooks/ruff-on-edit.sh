#!/usr/bin/env bash
# PostToolUse hook: format + lint-fix any Python file Claude just edited.
# Reports remaining lint errors back to Claude (exit 2) so it can fix them.
set -uo pipefail

file=$(jq -r '.tool_input.file_path // empty')
[[ "$file" == *.py && -f "$file" ]] || exit 0

cd "${CLAUDE_PROJECT_DIR:-.}"
if [[ -f pyproject.toml ]] && uv run --quiet ruff --version >/dev/null 2>&1; then
  ruff=(uv run --quiet ruff)
else
  ruff=(uvx --quiet ruff)
fi

out=$("${ruff[@]}" check --fix --quiet "$file" 2>&1)
status=$?
"${ruff[@]}" format --quiet "$file" >/dev/null 2>&1
if (( status != 0 )); then
  echo "ruff found issues in $file:" >&2
  echo "$out" >&2
  exit 2
fi
exit 0
