# Workspace Guidelines

- **Project Architecture**: SQS Lambda consumer in Go.
- **Safety**: Support dry-run if mutating AWS resources.
- **Logging**: Use `log/slog` for structured JSON logging.
- **AWS SDK v2**: Use context propagation, explicitly initialize clients, and use `%w` for error wrapping.
- **Commits**: Use Conventional Commits.
