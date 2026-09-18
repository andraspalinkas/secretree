# Contributing

Thanks for looking. A few things that keep this project coherent:

- **The format is the product.** Changes to what lands on the remote need
  a `docs/decisions.md` entry and, if hand-restore is affected, an update to
  `docs/restore-by-hand.md`. Breaking a v1 vault is a v2 with a migration.
- **No new cryptography.** age and OpenSSH signatures, nothing else.
- **Tests are real.** `internal/app` tests build the binary and drive git
  through it against local vaults. New behaviour comes with such a test.
- **Never weaken a verification to make a test pass.**

Build and test:

```sh
go vet ./... && go test ./...
go build -o bin/secretree ./cmd/secretree
```

Commit messages: what changed and why, in the imperative.
