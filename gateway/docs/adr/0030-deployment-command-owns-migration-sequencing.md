# Deployment command owns migration sequencing

The Kubernetes reference will run versioned SQL migrations in a release-specific Migration Job and wait for its successful completion before creating the gateway Deployment. The deploy command, rather than gateway startup or an assumed `kubectl apply` ordering, owns this sequence so the production image never applies migrations and a failed migration cannot roll out a new gateway Pod.

## Considered Options

- Apply migrations from every gateway Pod at startup.
- Apply all manifests together and rely on readiness.
- Run migrations in a dedicated Job but leave deployment ordering to the operator.
