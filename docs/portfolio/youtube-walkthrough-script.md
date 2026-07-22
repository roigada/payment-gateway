# YouTube walkthrough script

This is the recording plan for the 60–90 second unlisted YouTube walkthrough. Record against a local demo only. Do not display `.env`, credential generation, authorization headers, or card details.

## Storyboard

| Time | Screen | Narration or on-screen message |
| --- | --- | --- |
| 0–10s | Root README, then `make demo` completing | “A Go payment gateway demo focused on safe retries around an external bank operation.” |
| 10–30s | `make demo-smoke` output | “The smoke path proves the public API rejects unauthenticated requests, authorizes and captures a Payment, and verifies the final status.” |
| 30–45s | `demo/payment-gateway.http` Idempotency Replay request and response, with credentials and card values hidden | “Repeating the same command with the same Idempotency Key replays the original result instead of creating a second operation.” |
| 45–60s | Grafana Gateway Overview after the smoke run | “Gateway-owned metrics make HTTP and payment-operation activity visible without exposing Mock Bank internals.” |
| 60–80s | Root README recovery diagram or ADR 0022 | “Before the bank call, the gateway records a Bank Operation Key. If completion is interrupted, a same-key retry recovers that operation rather than duplicating the bank side effect.” |
| 80–90s | README reviewer path | “The README, OpenAPI contract, tests, runbook, and ADRs provide the evidence behind the demo.” |

## Recording checklist

- Use a disposable local runtime created by `make demo`.
- Crop or redact terminal output before publishing if any value resembles a credential, authorization header, card detail, or environment variable.
- Show only harmless HTTP request and response fields; do not record the `.env` file.
- Upload the final recording as **unlisted** on YouTube.
- Create a static poster image from a safe frame and link it to the YouTube URL from the root README before a portfolio release.
