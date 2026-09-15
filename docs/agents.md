# AI agents on pull requests

An agent is a member like any other device: it has its own age identity
and signing key, a person approves it, and everything it writes is signed.
That is not a workaround; it is what the model demands: to read you need a
key, to write you need a signer.

## Adding an agent

On the machine that will run it (usually the runner box):

```sh
secretgit join --vault git@gitlab.com:team/app-vault.git --name review-bot --out review-bot.txt
```

A person approves it **as an agent**:

```sh
secretgit member add --request review-bot.txt --role agent
```

Agents may read, comment and review. Their approvals never count toward
`required_approvals`, they cannot merge, and their comments carry an
`agent` mark in the UI and in `pr show`.

## Running one

The runner already executes a job for every new commit of every open pull
request, and hands the job the PR context:

| variable | meaning |
|---|---|
| `SECRETGIT_PR`, `SECRETGIT_PR_ID` | pull request number and id |
| `SECRETGIT_COMMIT` | the head commit being processed |
| `SECRETGIT_BASE`, `SECRETGIT_BASE_BRANCH`, `SECRETGIT_HEAD_BRANCH` | the base commit and branch names |
| `SECRETGIT_REPO_DIR` | the runner's clone, for `secretgit -C` calls |
| `SECRETGIT_BRANCH` | set instead of the PR variables for plain branch checks |

So an agent is a runner with a name and a command:

```sh
secretgit runner --name ai-review --branches "" --cmd agents/review.sh
```

`--branches ""` keeps the agent on pull requests only (by default a runner
also checks `main`).

`agents/review.sh` (in this repository) pipes the diff to a model, posts
line comments with `secretgit pr comment --path --line`, records a verdict
with `pr review --verdict`, and, because the diff left the key boundary,
writes a ledger entry first. Several agents are several runners with
different names (`security`, `tests`, `docs`); each gets its own key.

## Which model sees the code

This is the one place where something leaves the key boundary, and it is
explicit. A local model (`MODEL_CMD="ollama run codellama"`) keeps the diff
on the machine. A hosted model receives the diff; the ledger says so
(`secretgit ledger`), naming the PR, the commit and the provider. The team
decides which providers are acceptable and writes it down in
`.secretgit/policy.json` next to the review rules.

## Reading agent output

- `pr show 7` prints every comment with its file, line and the commit it
  was made on, marked `[agent]` where relevant, and `[resolved by …]` once a
  person resolves the thread (`pr resolve 7 <comment-id>`).
- The PR page shows comments inline under their diff line. A comment made
  on an earlier head is followed to its new line through the diff and
  marked "followed from line N of <commit>"; if its line was changed or
  removed it is shown in the conversation as **outdated** instead.
- Resolved threads collapse in the diff and dim in the conversation.

## Trust

An agent's key can do exactly what a junior reviewer's can: read, and say
things. It cannot approve on its own authority, merge, remove members, or
change policy. If the agent's machine is compromised, revoke it like any
device (`member remove --name review-bot`): past signatures stay valid,
future ones are refused.
