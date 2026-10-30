# 11. Run fixed dump scripts inside database containers

Date: 2026-10-30

## Status

Accepted

## Context

Manual backups ([#48](https://github.com/daniel-vuky/server-control-panel/issues/48), [#50](https://github.com/daniel-vuky/server-control-panel/issues/50), [#51](https://github.com/daniel-vuky/server-control-panel/issues/51)) dump a project's PostgreSQL, MySQL/MariaDB or MongoDB database and upload it to Google Drive. The dump tools live in the database's own image, and the credentials live in that container's environment: `POSTGRES_PASSWORD`, `MYSQL_ROOT_PASSWORD`, `MONGO_INITDB_ROOT_PASSWORD` and the like, or their Docker-secrets twins (`*_FILE`), which point at files only the container can read.

[ADR 5](0005-run-only-whitelisted-docker-compose-commands-without-a-shell.md) says no command runs through a shell. A dump needs a little logic inside the container: read a variable or its `_FILE` twin, pick `mariadb-dump` or `mysqldump`, write the password where the client reads it, remove it afterwards. There is no single argv that does this.

### Option A: Pass the credentials from the panel

The panel reads the project's `.env` and passes the password through `docker compose exec -e` or on the tool's command line.

- Con: the password crosses the panel and appears in a process's argv or environment on the host, where `ps` and `/proc` can read it.
- Con: it misses values set in `environment:` in the compose file, `env_file:` files and Docker secrets, which the panel never resolves (`compose config --no-env-resolution`).

### Option B: Run the dump tools from the panel's image over the network

- Con: the panel image would carry every client version, the database would have to accept a network login, and the credentials problem of option A stays.

### Option C: A fixed shell script per action, run inside the database's container

`docker compose exec -T {service} sh -c <SCRIPT> sh [{dbname}]`.

- Pro: the credentials never leave the container, and every way the owner set them (environment, `env_file`, `_FILE` secrets) is seen.
- Pro: the image's own client matches its server version.
- Con: a shell runs, which ADR 5 rules out for the panel.

## Decision

Option C, bounded so it does not open what ADR 5 closes:

1. **The shell runs inside the database's container, not the panel's.** The panel still starts only `docker` with an argv from the registry and no shell (ADR 5 decisions 2 and 3 hold).
2. **The script is a constant.** Each action (`db.pg.dump`, `db.pg.dump.default`, `db.pg.size`, `db.pg.size.default`, `db.mysql.dump`, `db.mysql.size`, `db.mongo.dump`, `db.mongo.size`, each with an `.override` variant) carries one reviewed script from `internal/runner/dbscripts.go` as one fixed argv element. Nothing is spliced into it, at run time or at build time.
3. **The only value is a validated name passed as `$1`.** The `{dbname}` slot accepts `^[A-Za-z0-9_][A-Za-z0-9_.$-]{0,63}$`, so it can never be a flag, a quote or shell syntax, and the script uses it only as `"$1"`. The one place it has to enter a query (the MySQL size read) checks it again with `case` first. The `.default` PostgreSQL actions have no slot: they dump the image's default database. The service is the usual `{service}` name slot.
4. **Passwords are never on argv and never exported.** They go into a `mktemp` file under `umask 077` that an `EXIT` trap removes (a signal becomes an exit, so the trap runs; the script never `exec`s its client):
   - PostgreSQL: the official image trusts the local socket, so the script tries it first with no password. Only if that fails and `POSTGRES_PASSWORD` is set does it write a `PGPASSFILE`, with `:` and `\` escaped.
   - MySQL and MariaDB: `--defaults-extra-file`, values double-quoted with `\` and `"` escaped. `MYSQL_PWD` is not used (deprecated in MySQL 9.7). A non-root MySQL user needs `--no-tablespaces` and `--set-gtid-purged=OFF`; MariaDB rejects the latter, so it goes only to MySQL's `mysqldump`.
   - MongoDB: `mongodump --config` with `password:` alone, the only key the tools' config accepts; the user name and `--authenticationDatabase admin` are on argv. `mongosh` has no config file, so the size read gets `U`, `P` and `N` in the environment of that one process, and its fixed JavaScript reads `process.env`.
5. **Dumps stream with a stall limit, not a timeout.** `Runner.RunTo` runs only the dump actions (`Run` refuses them): stdout goes straight to the caller's writer with no cap, stderr is capped at 4 MiB and masked. A dump fails after 5 minutes without a stdout byte and is killed at a 6-hour ceiling; a failed write stops it at once. The size actions are ordinary read commands through `Run`, with the 15-second timeout.

Verified on `postgres:17-alpine` (busybox `sh`), `mysql:9` (bash), `mariadb:11` and `mongo:7` (dash), with a non-root MySQL and MariaDB user, a MongoDB root user, `*_FILE` secrets and the password `p:a\ss"w'o#r d;$x`.

## Consequences

**Easier:**

- Backups work with however the owner set the credentials, without the panel ever holding them.
- The whole attack surface is six short constants and one name rule, reviewed in one file.

**Harder:**

- ADR 5's "no shell" now has an exception, limited to these registry entries; any change to a script needs the same review as a change to the registry.
- The scripts depend on the official images' variable names, a POSIX `sh`, `cat`, `sed` and `mktemp` in the image, and on the clients' option-file syntax. An image without them fails at Dump with the shell's error.
- Killing the `docker` client does not kill the process inside the container. A dump that stalls on a lock keeps running there until it next writes (and dies on the broken pipe) or finishes; the trap still removes its temp file.
- MySQL 9.7's `mysqldump` run by a non-root user reports an `Error:` line on stderr about masking policies and still exits 0; the dump is complete apart from those policies.
- `mongosh` sends its own telemetry by default, as it does for any `mongosh` run in that container (a healthcheck, say); the panel does not change the user's `mongosh` settings.
