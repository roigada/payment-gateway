## Agent skills

### Issue tracker

Issues and PRDs are tracked in GitHub Issues for `roigada/template-go` using the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the default triage label vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

This is a single-context repo: use root `CONTEXT.md` and root `docs/adr/` when present. See `docs/agents/domain.md`.

### Go test assertions

Use `github.com/stretchr/testify` as the default assertion library for Go tests. Prefer `require` for setup failures and checks that must stop the test, and `assert` for independent expectations where continuing gives better failure output.
