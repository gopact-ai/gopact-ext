# MySQL Store

`mysql.Open` returns a `*mysql.Store` facade over the shared `dbstore` implementation. Schema changes are explicit:

The continuously tested production baseline is MySQL 8.4.

```go
if err := mysql.Migrate(dsn); err != nil { // deployment migration stage, once
	return err
}
store, err := mysql.Open(dsn)
if err != nil {
	return err
}
defer store.Close()
```

For an existing schema, stop and drain every old writer before `Migrate`, and start only the new binaries after it succeeds. `GET_LOCK` serializes migration processes but does not stop an old application writer. Migration repairs and verifies v2 metadata before recording completion; rerun it as the explicit repair operation. `Open` verifies the version, critical columns and indexes, and the required InnoDB/`utf8mb4_bin` table options.

Run and Session IDs are byte-limited to 191 and may not end in a space so their identity is consistent across supported databases. Lease TTLs are materialized and checked against MySQL's clock inside the ownership transaction. Tune the returned `SQLDB()` pool for the service workload. Purge row/byte limits count logical history and encoded payload, not physical redo/binlog volume; pace cleanup and monitor MySQL's native log and replication retention. Long-running active Runs may use `PurgeConfirmedRunLog` only after explicitly accepting loss of old Retry/Fork/audit history.
