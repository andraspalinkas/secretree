#!/usr/bin/env bash
# secretgit tour: builds the binary and walks through every feature on a
# local vault with two "devices" (alice and bob) that have separate key
# stores. Nothing touches your real Keychain or any remote host.
#
#   scripts/tour.sh            # run the tour, leave the UI running
#   scripts/tour.sh --clean    # remove the tour directory
set -euo pipefail

TOUR="${SECRETGIT_TOUR_DIR:-$HOME/secretgit-tour}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
if [ "${1:-}" = "--clean" ]; then
  pkill -f "secretgit ui --listen 127.0.0.1:7391" 2>/dev/null || true
  rm -rf "$TOUR"; echo "removed $TOUR"; exit 0
fi

step() { printf '\n\033[1;32m== %s\033[0m\n' "$*"; }
run()  { printf '\033[2m$ %s\033[0m\n' "$*"; "$@"; }

export PATH="$HOME/sdk/go/bin:$HOME/go/bin:$PATH"
step "build"
( cd "$REPO" && go build -trimpath -ldflags="-s -w" -o bin/secretgit ./cmd/secretgit )
ln -sf "$REPO/bin/secretgit" "$REPO/bin/git-remote-secretgit"
export PATH="$REPO/bin:$PATH"

rm -rf "$TOUR"; mkdir -p "$TOUR"
export SECRETGIT_KEYSTORE=file
export GIT_CONFIG_PARAMETERS="'commit.gpgsign=false' 'init.defaultBranch=main'"
alice() { SECRETGIT_HOME="$TOUR/alice-keys" GIT_AUTHOR_NAME=alice GIT_AUTHOR_EMAIL=alice@example.com GIT_COMMITTER_NAME=alice GIT_COMMITTER_EMAIL=alice@example.com "$@"; }
bob()   { SECRETGIT_HOME="$TOUR/bob-keys"   GIT_AUTHOR_NAME=bob   GIT_AUTHOR_EMAIL=bob@example.com   GIT_COMMITTER_NAME=bob   GIT_COMMITTER_EMAIL=bob@example.com   "$@"; }

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
printf '# app\n\nA demo project kept in a secretgit vault.\n' > README.md
printf '#!/bin/sh\nset -e\necho "building…"\ntest -f README.md && echo "tests: 12 passed"\n' > .secretgit_ci_tmp
mkdir -p .secretgit && mv .secretgit_ci_tmp .secretgit/ci && chmod +x .secretgit/ci
alice git add -A && alice git commit -qm "initial"

step "2. one command: vault + keys + helper + origin + first push"
run alice secretgit init --vault "$TOUR/vault.git" --label app --kit-out "$TOUR/alice-recovery-kit.txt" --push
run alice secretgit kit --confirm
run alice secretgit status

step "3. what the host sees (nothing readable)"
run git -C "$TOUR/vault.git" ls-tree -r --name-only HEAD
run git -C "$TOUR/vault.git" log --format='%h %s' | head -3

step "4. bob joins from his own device"
run bob secretgit join --vault "$TOUR/vault.git" --name bob-laptop --out "$TOUR/bob-join-request.txt"
run alice secretgit member add --request "$TOUR/bob-join-request.txt"
run alice secretgit member list
mkdir -p "$TOUR/bob" && cd "$TOUR/bob"
run bob secretgit clone "$TOUR/vault.git" app
cd "$TOUR/bob/app" && run bob git log --oneline

step "5. review policy: one approval and a green ci check"
cd "$TOUR/alice/app"
run alice secretgit policy --approvals 1 --checks ci
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
run bob secretgit pr open --title "Add retry helper" --body "Wraps flaky calls. Delay is fixed for now."

step "7. alice reviews (a line comment, then approval); ci runs where the key is"
cd "$TOUR/alice/app"
run alice secretgit pr comment 1 -m "Should the delay grow between attempts?" --path src/retry.go --line 12
run alice secretgit pr approve 1 -m "Fine as a first version."
run alice secretgit runner --once
run alice secretgit pr show 1

step "8. merge (policy enforced), deploy (only with a green check)"
alice git checkout -q main
run alice secretgit pr merge 1
run alice secretgit runner --once
run alice secretgit deploy-agent --to "$TOUR/deployed" --once --cmd 'echo "restarted $(date)" > .deployed-at'
ls "$TOUR/deployed"

step "9. share a file with someone who has no key; the ledger records it"
run alice secretgit share src/retry.go --note "for the auditor" --out "$TOUR/share.html" | tee "$TOUR/share.log"
run alice secretgit ledger

step "10. backup with restore proof, then a disaster restore from the kit alone"
run alice secretgit backup
mkdir -p "$TOUR/new-machine-keys"
run env SECRETGIT_HOME="$TOUR/new-machine-keys" secretgit restore --vault "$TOUR/vault.git" --to "$TOUR/restored" --from-recovery-kit "$TOUR/alice-recovery-kit.txt"
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
if env SECRETGIT_HOME="$TOUR/new-machine-keys" secretgit restore --vault "$TOUR/vault-evil.git" --to "$TOUR/restored-evil" 2>"$TOUR/evil.log"; then echo "!! tampering not detected"; exit 1; else echo "tampering detected: $(tail -1 "$TOUR/evil.log")"; fi
run alice secretgit verify --quick --all

step "12. the local UI"
pkill -f "secretgit ui --listen 127.0.0.1:7391" 2>/dev/null || true
cd "$TOUR/alice/app"
( alice nohup secretgit ui --listen 127.0.0.1:7391 > "$TOUR/ui.log" 2>&1 & )
sleep 1
KEY=$(grep 'key:' "$TOUR/share.log" | awk '{print $2}')
cat <<MSG

Everything is under $TOUR

  UI (alice's view):        http://127.0.0.1:7391          branches, files, blame, search
                            http://127.0.0.1:7391/pulls    the merged PR with its inline comment and check
  Permalink:                $(alice secretgit link src/main.go:6)
  Share page (no key):      file://$TOUR/share.html#$KEY
  Recovery kit:             $TOUR/alice-recovery-kit.txt
  The vault as the host:    git -C $TOUR/vault.git ls-tree -r --name-only HEAD

Try next, in $TOUR/bob/app (bob) or $TOUR/alice/app (alice):
  bob secretgit pr open ...     (use: SECRETGIT_KEYSTORE=file SECRETGIT_HOME=$TOUR/bob-keys secretgit ...)
  secretgit watch --desktop     notifications about the other device's activity
  secretgit member remove --name bob-laptop ; then bob's next fetch fails
  secretgit verify              full rebuild proof from the remote

Stop the UI:  pkill -f 'secretgit ui'      Remove everything:  scripts/tour.sh --clean
MSG
