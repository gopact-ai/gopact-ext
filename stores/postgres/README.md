# PostgreSQL Store

`postgres.Open` returns a `*postgres.Store` facade over the shared `dbstore` implementation.

The continuously tested production baseline is PostgreSQL 16.

```go
if err := postgres.Migrate(dsn); err != nil { // deployment migration stage, once
	return err
}
store, err := postgres.Open(dsn)
if err != nil {
	return err
}
defer store.Close()
```

For an existing schema, stop and drain every old writer before `Migrate`, and start only the new binaries after it succeeds. PostgreSQL advisory locking serializes migration processes but does not stop an old application writer. Migration repairs and verifies v2 metadata before recording completion; rerun it as the explicit repair operation. `Open` verifies the version and critical columns and indexes.

Lease TTLs are materialized and checked against PostgreSQL's clock inside the ownership transaction. Tune the returned `SQLDB()` pool for the service workload. Purge row/byte limits count logical history and encoded payload, not physical WAL volume; pace cleanup and monitor WAL retention, replication slots, backups, and vacuum health. Long-running active Runs may use `PurgeConfirmedRunLog` only after explicitly accepting loss of old Retry/Fork/audit history.
