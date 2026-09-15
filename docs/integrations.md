# Integrations: the same tools, run where the key is

Most "marketplace" integrations are programs that read your code. Under
secretgit they run as jobs on your runner and report back as signed checks
or pull requests. Nothing changes for the tool; what changes is who gets to
read the tree.

The runner executes `.secretgit/ci` (or `make ci`, or `--cmd`). Add steps
there. Each step can create extra checks by running `secretgit`
itself (the runner's clone holds a key), or simply fail the job.

## Dependency updates (Renovate)

```sh
# in .secretgit/ci, or as a separate runner with --name deps --cmd:
npx renovate --platform=local --onboarding=false
```

Renovate's `local` platform prints what it would update; to open real PRs,
use its `--platform=github`-style flow against your *mirror* by pointing
`RENOVATE_ENDPOINT` at a local Gitea if you run one, or script it: create a
branch, commit the bump, `git push origin`, then `secretgit pr open`. A
`secretgit integrate renovate` recipe that wraps this is planned.

## Static analysis and security scanning

```sh
semgrep ci --config auto            # findings fail the job
trivy fs --exit-code 1 .            # vulnerable dependencies fail the job
codeql database create db --language=go && codeql database analyze db --format=sarif-latest --output=out.sarif
```

Output stays in the encrypted check log; nothing is uploaded to a scanner
vendor unless you add that step deliberately (and record it in the ledger).

## Coverage and test reports

Print the numbers into the log; they are visible on the PR page. To send a
*derived* metric (a percentage, a count) to an external dashboard, do it
from the pipeline explicitly. That is a disclosure: keep it to numbers.

## LLM code review

```sh
git diff "$SECRETGIT_COMMIT~1..$SECRETGIT_COMMIT" | my-review-tool > review.txt
secretgit pr comment "$PR" -m "$(cat review.txt)"
```

You choose the provider and the diff. The difference from a marketplace
app is not that nobody sees the code; it is that nobody sees it as a side
effect of hosting. Record the provider in `.secretgit/policy.json` or the
ledger so the team knows.

## Deploy previews

Run `secretgit deploy-agent --branch <pr-branch> --to /srv/previews/<n>` on
a preview host you control. Previews are builds, and builds are code: keep
them behind the same network boundary as the UI.

## Notifications

The host sees pushes to the vault repository and can fire a webhook with
nothing but "something changed". Point it at a tiny relay that posts to
ntfy, Slack or e-mail; the recipient runs `secretgit pr list` or opens the
local UI. No content transits the notifier.
