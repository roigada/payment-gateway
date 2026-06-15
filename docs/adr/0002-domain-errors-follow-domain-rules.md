# Domain errors follow domain rules

Task invariant errors live with the domain model, while use-case outcomes live in the application layer. `ErrInvalidTaskTitle` belongs to `internal/domain` because title validity is a Task rule, while `ErrTaskNotFound` belongs to `internal/app` because lookup failure is an application outcome that adapters translate into transport-specific responses.
