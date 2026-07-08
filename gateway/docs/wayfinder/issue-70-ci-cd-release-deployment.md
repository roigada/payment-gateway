# Assess CI/CD Release and Deployment Options

Research ticket: [Assess CI/CD release and deployment options](https://github.com/roigada/payment-gateway/issues/70)

## Question

What CI, release, and deployment work would add the strongest backend portfolio signal for this repo, given that it currently has local Docker Compose, tests, migrations, and a demo smoke script but no GitHub Actions workflow or hosted deployment story?

## Local Findings

- The repository is already positioned as a portfolio demo system: the root is the runnable demo surface, with `gateway/` as authored gateway code and `mock-bank/` as bundled third-party demo infrastructure. Source: [README.md](../../../README.md), [ADR-0021](../adr/0021-use-demo-monorepo-layout.md).
- Local reviewer startup is already strong: `make demo` runs Postgres, migrations, Mock Bank, the gateway API, Prometheus, and Grafana; `make demo-smoke` validates readiness, Authorize, Capture, Fetch, and final `captured` Payment Status. Source: [README.md](../../../README.md), [demo/smoke.sh](../../../demo/smoke.sh), [compose.yaml](../../../compose.yaml).
- The gateway has a real test suite across domain, app, HTTP, Mock Bank adapter, observability, and Postgres persistence. `GOCACHE=/private/tmp/payment-gateway-go-cache go test ./...` passes from `gateway/`. Source: local test run on 2026-07-08.
- There is no `.github/workflows` directory, no production gateway `Dockerfile`, no release image, and no hosted deployment target. Source: local file inventory on 2026-07-08.
- ADR-0007 intentionally kept production packaging out of the initial developer Compose decision. That makes production packaging a clean next portfolio signal rather than a contradiction of earlier architecture. Source: [ADR-0007](../adr/0007-developer-compose-for-local-runtime.md).

## External Findings

- GitHub Actions supports Go build/test workflows using the same local commands such as `go build` and `go test`; `actions/setup-go` has dependency caching enabled by default and supports `cache-dependency-path` for subdirectory modules. Source: [GitHub Docs: Building and testing Go](https://docs.github.com/en/actions/tutorials/build-and-test-code/go).
- GitHub Actions supports PostgreSQL service containers with health checks on Ubuntu/Linux runners, which fits a Postgres-backed gateway integration job. Source: [GitHub Docs: Creating PostgreSQL service containers](https://docs.github.com/en/actions/tutorials/use-containerized-services/create-postgresql-service-containers).
- Docker publishes official GitHub Actions for building and pushing images, setting up Buildx, logging into registries, generating metadata, setting up Compose, and scanning images. Source: [Docker Docs: Docker Build GitHub Actions](https://docs.docker.com/build/ci/github-actions/).
- GitHub deployment environments make deployment targets visible on the repository and can gate deployment jobs, restrict branches, and limit environment secrets. Source: [GitHub Docs: Control deployments](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/control-deployments).
- GitHub Actions OIDC lets workflows authenticate to cloud providers without storing long-lived cloud credentials as GitHub secrets, provided the cloud provider trust policy constrains which repos/branches/environments can receive tokens. Source: [GitHub Docs: OIDC in cloud providers](https://docs.github.com/en/actions/how-tos/secure-your-work/security-harden-deployments/oidc-in-cloud-providers).

## Options Considered

1. **CI quality gate only**
   - Add GitHub Actions for formatting, `go test ./...`, and possibly `go test -race ./...`.
   - Strong signal because it proves repeatable verification on every change.
   - Lower demo value if it stops at unit/integration tests and does not exercise the root demo stack.

2. **CI plus Compose smoke**
   - Add a workflow that runs the normal Go suite, starts the root Compose demo, waits for readiness, and runs `make demo-smoke`.
   - Stronger portfolio signal than Go-only CI because it validates the actual reviewer path: migrations, Postgres, Mock Bank, gateway, and public API behavior.
   - Risk: more moving pieces in CI. Keep it as a separate job from fast Go tests so failures are easier to diagnose.

3. **Production gateway image and release workflow**
   - Add a production `Dockerfile` for the authored gateway, then build and publish a versioned image through GitHub Actions.
   - Strong signal because it shows packaging, runtime boundary clarity, supply-chain awareness, and a deployable artifact.
   - This should not replace the root demo Compose stack. The demo stack can keep using source-mounted `go run`; the production image proves deployability.

4. **Hosted deployment**
   - Deploy the released gateway image plus Postgres and Mock Bank to a cloud or platform environment.
   - Potentially high visible signal, but cost, credential setup, uptime, data reset, and Mock Bank hosting make it heavier than the current portfolio need.
   - This is weaker than CI/release as an immediate next item unless the reviewer-demo ticket decides that a live URL is required for comprehension.

5. **Local-only release workflow**
   - Document a reproducible local release path without publishing or hosting anything.
   - Useful, but less convincing than a real GitHub Actions build because reviewers cannot see the automation history and artifacts.

## Decision

The strongest CI/CD roadmap candidate is **CI plus Compose smoke**, followed by **production gateway image and release workflow**.

Recommended order for the ranking ticket:

1. Add GitHub Actions quality gates for the gateway module: checkout, setup Go using `gateway/go.sum` as the cache dependency path, run `go test ./...`, and optionally add `go test -race ./...` if runtime is acceptable.
2. Add a separate Compose smoke job that starts the root demo stack and runs `make demo-smoke`, preserving the existing reviewer path as the contract.
3. Add a production gateway `Dockerfile` and a release workflow that builds the image on main/tags, produces registry metadata, and publishes a versioned artifact.
4. Defer hosted deployment until after the reviewer-demo decision. If hosting is chosen later, prefer GitHub environments plus OIDC over long-lived cloud credentials.

This order gives hiring reviewers the highest backend signal per unit of implementation effort: automated tests, database-backed integration proof, reproducible runtime packaging, and a credible release path. A live hosted deployment is not the first CI/CD priority for this map because the repository already optimizes for one-command local review, and hosting would add operational overhead before it adds much new evidence.

## Implications for Later Specs

- CI should treat `gateway/` as the Go module root and should account for the current subdirectory `go.sum`.
- The Compose smoke job should validate the root demo contract, not invent a second test path.
- Production packaging should be for the gateway service only; the bundled Mock Bank remains demo infrastructure with an explicit authorship boundary.
- Hosted deployment can be ranked later as optional polish, not as a prerequisite for a strong backend portfolio roadmap.
