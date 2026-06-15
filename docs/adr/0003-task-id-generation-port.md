# Task ID generation port

The application layer declares a `TaskIDGenerator` interface and the UUID-backed implementation lives in `internal/uuidgen`. This keeps domain identity generation independent from UUID libraries while giving the template a small example of an outbound port that is not persistence.
