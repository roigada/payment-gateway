# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`gateway/CONTEXT.md`** for gateway domain language.
- **`gateway/docs/adr/`** for architectural decisions that touch the gateway.

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The producer skill (`/grill-with-docs`) creates them lazily when terms or decisions actually get resolved.

## File structure

Gateway context inside the demo repo:

```
/
|-- gateway/
|   |-- CONTEXT.md
|   |-- docs/
|   |   `-- adr/
|   |       |-- 0001-example-decision.md
|   |       `-- 0002-example-follow-up.md
|   |-- cmd/
|   `-- internal/
`-- README.md
```

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `gateway/CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use, or there's a real gap to note for `/grill-with-docs`.

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> Contradicts ADR-0007 (example decision), but worth reopening because...
