# Application use-case input naming

Payment application use cases use intent-based input names: mutating requests are `Command` types and read requests are `Query` types. This keeps the app boundary consistent with the gateway's CQRS vocabulary while leaving lower-level adapters free to use storage-specific names when they describe implementation details rather than use-case requests.
