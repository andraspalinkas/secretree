#!/usr/bin/env bash
# secretree tour: builds the binary and walks through every feature on a
# local vault with two "devices" (alice and bob) that have separate key
# stores. Nothing touches your real Keychain or any remote host.
#
#   scripts/tour.sh            # run the tour, leave the UI running
#   scripts/tour.sh --clean    # remove the tour directory
set -euo pipefail

TOUR="${SECRETREE_TOUR_DIR:-$HOME/secretree-tour}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
if [ "${1:-}" = "--clean" ]; then
  pkill -f "secretree ui --listen 127.0.0.1:7391" 2>/dev/null || true
  rm -rf "$TOUR"; echo "removed $TOUR"; exit 0
fi

step() { printf '\n\033[1;32m== %s\033[0m\n' "$*"; }
run()  { printf '\033[2m$ %s\033[0m\n' "$*"; "$@"; }

export PATH="$HOME/sdk/go/bin:$HOME/go/bin:$PATH"
step "build"
( cd "$REPO" && go build -trimpath -ldflags="-s -w" -o bin/secretree ./cmd/secretree )
ln -sf "$REPO/bin/secretree" "$REPO/bin/git-remote-secretree"
export PATH="$REPO/bin:$PATH"

rm -rf "$TOUR"; mkdir -p "$TOUR"
export SECRETREE_KEYSTORE=file
export GIT_CONFIG_PARAMETERS="'commit.gpgsign=false' 'init.defaultBranch=main'"
alice() { SECRETREE_HOME="$TOUR/alice-keys" GIT_AUTHOR_NAME=alice GIT_AUTHOR_EMAIL=alice@example.com GIT_COMMITTER_NAME=alice GIT_COMMITTER_EMAIL=alice@example.com "$@"; }
bob()   { SECRETREE_HOME="$TOUR/bob-keys"   GIT_AUTHOR_NAME=bob   GIT_AUTHOR_EMAIL=bob@example.com   GIT_COMMITTER_NAME=bob   GIT_COMMITTER_EMAIL=bob@example.com   "$@"; }

step "1. alice creates a project"
mkdir -p "$TOUR/alice/app/src" && cd "$TOUR/alice/app"
git init -q
cat > src/main.go <<'GO'
package main

import "fmt"

func main() {
	fmt.Println("hello from the vault")
}
GO
printf '# app\n\nA demo project kept in a secretree vault.\n' > README.md
printf '#!/bin/sh\nset -e\necho "building…"\ntest -f README.md && echo "tests: 12 passed"\n' > .secretree_ci_tmp
mkdir -p .secretree && mv .secretree_ci_tmp .secretree/ci && chmod +x .secretree/ci
alice git add -A && alice git commit -qm "initial"

step "2. one command: vault + keys + helper + origin + first push"
run alice secretree init --vault "$TOUR/vault.git" --label app --kit-out "$TOUR/alice-recovery-kit.txt" --push
run alice secretree kit --confirm
run alice secretree status

step "3. what the host sees (nothing readable)"
run git -C "$TOUR/vault.git" ls-tree -r --name-only HEAD
run git -C "$TOUR/vault.git" log --format='%h %s' | head -3

step "4. bob joins from his own device"
run bob secretree join --vault "$TOUR/vault.git" --name bob-laptop --out "$TOUR/bob-join-request.txt"
run alice secretree member add --request "$TOUR/bob-join-request.txt"
run alice secretree member list
mkdir -p "$TOUR/bob" && cd "$TOUR/bob"
run bob secretree clone "$TOUR/vault.git" app
cd "$TOUR/bob/app" && run bob git log --oneline

step "5. review policy: one approval and a green ci check"
cd "$TOUR/alice/app"
run alice secretree policy --approvals 1 --checks ci
alice git add -A && alice git commit -qm "review policy" && alice git push -q origin main

step "6. bob opens a pull request"
cd "$TOUR/bob/app"
bob git pull -q --ff-only origin main
bob git checkout -qb feature/retry
cat > src/retry.go <<'GO'
package main

import "time"

// retry calls fn up to n times, sleeping between attempts.
func retry(n int, delay time.Duration, fn func() error) error {
	var err error
	for i := 0; i < n; i++ {
		if err = fn(); err == nil {
			return nil
		}
		time.Sleep(delay)
	}
	return err
}
GO
bob git add -A && bob git commit -qm "add retry helper" && bob git push -q origin feature/retry
run bob secretree pr open --title "Add retry helper" --body "Wraps flaky calls. Delay is fixed for now."

step "7. alice reviews (a line comment, then approval); ci runs where the key is"
cd "$TOUR/alice/app"
run alice secretree pr comment 1 -m "Should the delay grow between attempts?" --path src/retry.go --line 12
run alice secretree pr approve 1 -m "Fine as a first version."
run alice secretree runner --once
run alice secretree pr show 1

step "7b. an AI review agent joins (role: agent) and reviews through the runner"
mkdir -p "$TOUR/bot"
bot() { SECRETREE_HOME="$TOUR/bot-keys" GIT_AUTHOR_NAME=review-bot GIT_AUTHOR_EMAIL=bot@example.com GIT_COMMITTER_NAME=review-bot GIT_COMMITTER_EMAIL=bot@example.com "$@"; }
run bot secretree join --vault "$TOUR/vault.git" --name review-bot --out "$TOUR/bot-join-request.txt"
run alice secretree member add --request "$TOUR/bot-join-request.txt" --role agent
cd "$TOUR/bot" && run bot secretree clone "$TOUR/vault.git" app
# a stand-in for a model: agents/review.sh does the same with `claude -p`
cat > "$TOUR/fake-review.sh" <<'SH'
#!/bin/sh
set -e
[ -n "${SECRETREE_PR:-}" ] || { echo "not a pull request; nothing to review"; exit 0; }
secretree -C "$SECRETREE_REPO_DIR" ledger add --kind export --subject "diff of PR #$SECRETREE_PR @$SECRETREE_COMMIT → fake-model" --note "ai review" >/dev/null
secretree -C "$SECRETREE_REPO_DIR" pr comment "$SECRETREE_PR" --path src/retry.go --line 13 -m "Consider exponential backoff instead of a fixed delay." >/dev/null
secretree -C "$SECRETREE_REPO_DIR" pr review "$SECRETREE_PR" --verdict comment -m "No blocking issues; one suggestion inline." >/dev/null
echo "review posted"
SH
chmod +x "$TOUR/fake-review.sh"
cd "$TOUR/bot/app" && run bot secretree runner --once --name ai-review --branches "" --cmd "$TOUR/fake-review.sh"
cd "$TOUR/alice/app" && run alice secretree pr show 1 | sed -n '1,4p;/agent/p'

step "8. merge (policy enforced), deploy (only with a green check)"
alice git checkout -q main
run alice secretree pr merge 1
run alice secretree runner --once
run alice secretree deploy-agent --to "$TOUR/deployed" --once --cmd 'echo "restarted $(date)" > .deployed-at'
ls "$TOUR/deployed"

step "9. share a file with someone who has no key; the ledger records it"
run alice secretree share src/retry.go --note "for the auditor" --out "$TOUR/share.html" | tee "$TOUR/share.log"
run alice secretree ledger

step "10. backup with restore proof, then a disaster restore from the kit alone"
run alice secretree backup
mkdir -p "$TOUR/new-machine-keys"
run env SECRETREE_HOME="$TOUR/new-machine-keys" secretree restore --vault "$TOUR/vault.git" --to "$TOUR/restored" --from-recovery-kit "$TOUR/alice-recovery-kit.txt"
diff -rq --exclude=.git "$TOUR/alice/app" "$TOUR/restored" && echo "restored tree is identical"

step "11. tamper test: a hostile host flips a byte"
cp -r "$TOUR/vault.git" "$TOUR/vault-evil.git"
git -C "$TOUR/vault-evil.git" config receive.denyNonFastForwards false
EVIL="$TOUR/evil" && git clone -q "$TOUR/vault-evil.git" "$EVIL"
F=$(cd "$EVIL" && ls repos/*/*.bundle.age | sort | tail -1)   # the newest bundle, one every restore needs
python3 - "$EVIL/$F" <<'PY'
import sys; p=sys.argv[1]; b=bytearray(open(p,'rb').read()); b[len(b)//2]^=0xff; open(p,'wb').write(b)
PY
( cd "$EVIL" && git commit -qam "tamper" && git push -q origin HEAD:main )
if env SECRETREE_HOME="$TOUR/new-machine-keys" secretree restore --vault "$TOUR/vault-evil.git" --to "$TOUR/restored-evil" 2>"$TOUR/evil.log"; then echo "!! tampering not detected"; exit 1; else echo "tampering detected: $(tail -1 "$TOUR/evil.log")"; fi
run alice secretree verify --quick --all

step "12. the local UI"
pkill -f "secretree ui --listen 127.0.0.1:7391" 2>/dev/null || true
cd "$TOUR/alice/app"
( alice nohup secretree ui --listen 127.0.0.1:7391 > "$TOUR/ui.log" 2>&1 & )
sleep 1
KEY=$(grep 'key:' "$TOUR/share.log" | awk '{print $2}')
cat <<MSG

Everything is under $TOUR

  UI (alice's view):        http://127.0.0.1:7391          branches, files, blame, search
                            http://127.0.0.1:7391/pulls    the merged PR: inline comments (one by the agent), checks, Resolve
  Permalink:                $(alice secretree link src/main.go:6)
  Share page (no key):      file://$TOUR/share.html#$KEY
  Recovery kit:             $TOUR/alice-recovery-kit.txt
  The vault as the host:    git -C $TOUR/vault.git ls-tree -r --name-only HEAD

Try next, in $TOUR/bob/app (bob) or $TOUR/alice/app (alice):
  bob secretree pr open ...     (use: SECRETREE_KEYSTORE=file SECRETREE_HOME=$TOUR/bob-keys secretree ...)
  secretree watch --desktop     notifications about the other device's activity
  secretree member remove --name bob-laptop ; then bob's next fetch fails
  secretree verify              full rebuild proof from the remote

Stop the UI:  pkill -f 'secretree ui'      Remove everything:  scripts/tour.sh --clean
MSG
