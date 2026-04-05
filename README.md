# gocrud

A Go library that generates a gRPC CRUD service from a Protocol Buffer entity definition, backed by SQL.

## Concept

Given a protobuf message describing an entity, **gocrud** produces a fully functional gRPC service that translates standard Create/Read/Update/Delete/List operations into SQL queries — without requiring the user to write boilerplate service code or SQL by hand.

```
  Your .proto entity
        │
        ▼
  [ gocrud ]
        │
        ├─► gRPC service implementation
        └─► SQL query layer
```

## Status

Early design phase. The core concept is settled; several integration details are still being decided (see [Open Questions](#open-questions)).

## Planned Features

- Automatic gRPC service generation from a protobuf entity message
- SQL query generation for standard CRUD + List (with filtering and pagination)
- Support for multiple SQL dialects (PostgreSQL, MySQL, Oracle, SQL Server, Cloud Spanner, …)
- Configurable server (port, TLS, interceptors)

## Open Questions

The following design decisions have not been finalised:

| Area | Question |
|------|----------|
| **Code generation** | Which parts are fully generated vs. provided as a runtime library to be configured by the caller? |
| **SQL dialect** | How is the target dialect specified — build-time flag, config file, programmatic option? |
| **Database config** | How are connection details (DSN, pool settings) supplied? |
| **Server config** | How are port, TLS, and gRPC options configured? |
| **Schema ownership** | Does gocrud manage migrations, or does it assume the schema already exists? |

## Build

This project uses [Bazel](https://bazel.build/) with [Bzlmod](https://bazel.build/external/bzlmod).

```sh
# Build everything
bazel build //...

# Run tests
bazel test //...
```

Go tooling also works directly:

```sh
go build ./...
go test ./...
```

## License

Apache 2.0 — see [LICENSE](LICENSE).
