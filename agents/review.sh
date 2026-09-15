#!/bin/sh
# secretgit review agent: run by `secretgit runner --name ai-review --cmd agents/review.sh`
# on a machine that holds the agent's key. For every new commit of an open
# pull request it sends the diff to a model, posts line comments, records a
# verdict, and writes a ledger entry because the diff left the key boundary.
#
# Requires: the Claude Code CLI (`claude`) and `jq`. Swap the MODEL_CMD line
# for any other provider, or for a local model (ollama run …) to keep the
# diff inside the key boundary entirely.
set -eu
[ -n "${SECRETGIT_PR:-}" ] || { echo "not a pull request commit; nothing to review"; exit 0; }

MODEL_CMD=${MODEL_CMD:-"claude -p --output-format text"}
PROVIDER=${PROVIDER:-claude}
PROMPT='You are reviewing a pull request diff. Reply with JSON lines only, no prose.
For each concrete issue: {"path":"<file path from the +++ header>","line":<line number in the new file>,"comment":"<one or two sentences>"}
Finish with exactly one line: {"verdict":"approve"|"request_changes"|"comment","summary":"<one sentence>"}
Only comment on lines that appear as + lines in the diff.'

diff=$(git diff "${SECRETGIT_BASE}...${SECRETGIT_COMMIT}")
[ -n "$diff" ] || { echo "empty diff"; exit 0; }

# the diff leaves the key boundary: say so where the team can see it
secretgit -C "$SECRETGIT_REPO_DIR" ledger add --kind export --subject "diff of PR #${SECRETGIT_PR} @${SECRETGIT_COMMIT} → ${PROVIDER}" --note "ai review" >/dev/null

out=$(printf '%s\n\n%s\n' "$PROMPT" "$diff" | sh -c "$MODEL_CMD")
posted=0
echo "$out" | while IFS= read -r line; do
  case "$line" in
    \{*\"verdict\"*)
      v=$(printf '%s' "$line" | jq -r .verdict); s=$(printf '%s' "$line" | jq -r .summary)
      secretgit -C "$SECRETGIT_REPO_DIR" pr review "$SECRETGIT_PR" --verdict "$v" -m "$s"
      echo "verdict: $v — $s";;
    \{*\"path\"*)
      p=$(printf '%s' "$line" | jq -r .path); l=$(printf '%s' "$line" | jq -r .line); c=$(printf '%s' "$line" | jq -r .comment)
      secretgit -C "$SECRETGIT_REPO_DIR" pr comment "$SECRETGIT_PR" --path "$p" --line "$l" -m "$c" >/dev/null
      echo "comment: $p:$l";;
  esac
done
echo "review posted for #${SECRETGIT_PR} @${SECRETGIT_COMMIT}"
