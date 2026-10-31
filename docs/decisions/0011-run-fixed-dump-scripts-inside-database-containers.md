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
2. **The script is a constant.** Each action (`db.pg.dump`, `db.pg.dump.default`, `db.pg.size`, `db.pg.size.default`, `db.mysql.dump`, `db.mysql.size`, `db.mongo.dump`, `db.mongo.size` and `db.dump.stop`, each with an `.override` variant) carries one reviewed script from `internal/runner/dbscripts.go` as one fixed argv element. Nothing is spliced into it, at run time or at build time.
3. **The only value is a validated name passed as `$1`.** The `{dbname}` slot accepts `^[A-Za-z0-9_][A-Za-z0-9_.$-]{0,63}$`, so it can never be a flag, a quote or shell syntax, and the script uses it only as `"$1"`. The one place it has to enter a query (the MySQL size read) checks it again with `case` first. The `.default` PostgreSQL actions have no slot: they dump the image's default database. The service is the usual `{service}` name slot.
4. **Passwords are never on argv and never exported.** They go into a `mktemp` file under `umask 077` that an `EXIT` trap removes (a signal becomes an exit, so the trap runs; the script never `exec`s its client):
   - PostgreSQL: the official image trusts the local socket, so the script tries it first with no password. Only if that fails and `POSTGRES_PASSWORD` is set does it write a `PGPASSFILE`, with `:` and `\` escaped.
   - MySQL and MariaDB: `--defaults-extra-file`, values double-quoted with `\` and `"` escaped. `MYSQL_PWD` is not used (deprecated in MySQL 9.7). A non-root MySQL user needs `--no-tablespaces` and `--set-gtid-purged=OFF`; MariaDB rejects the latter, so it goes only to MySQL's `mysqldump`.
   - MongoDB: `mongodump --config` with `password:` alone, the only key the tools' config accepts; the user name and `--authenticationDatabase admin` are on argv. `mongosh` has no config file, so the size read gets `U`, `P` and `N` in the environment of that one process, and its fixed JavaScript reads `process.env`.
5. **A dump the panel gives up on is stopped inside the container.** Killing the `docker` client does not kill the process inside the container: dockerd keeps draining the exec's stdout. So the dump scripts run the dumper as a child (`&`, then `wait`), write `<shell pid> <dumper pid>` to `/tmp/.scp-dump.pid` (a `mktemp` file renamed over the path, so a planted symlink is replaced, never written through), forward `HUP`/`INT`/`TERM`/`PIPE` to the dumper and wait for it, and remove the file in the `EXIT` trap with the credentials file. A dump refuses to start, before reading any credential, while the file names a live dump: `a previous dump is still running` on stderr, exit 1 (one backup runs panel-wide, ADR 7, so such a dump is an orphan). A stale file (dead or reused pids) is replaced. `db.dump.stop` runs a fixed stop script with no value: it reads the file and signals a pid only when `/proc/<pid>` is a live `pg_dump`, `mysqldump`, `mariadb-dump` or `mongodump` (by `comm`, not a zombie) or a shell whose command line holds the pid file's path (only the dump scripts' text does): `TERM` and `CONT` to the dumper, up to 5 s, then `KILL`; the shell then exits by itself and cleans up (else `TERM`, then `KILL`). It removes the file when nothing is left, else says `the previous dump did not stop` and keeps it. At most about 10 s; it runs through `Run` with the 15 s read timeout. `internal/backup` runs it, best effort, on a context the job's cancellation does not end (bounded at 15 s), whenever the Dump step ends without the dump exiting by itself: a stall, the ceiling, a failed write, a cancel (`Close`), a client that did not run, or a panic in `RunTo`.
6. **Dumps stream with a stall limit, not a timeout.** `Runner.RunTo` runs only the dump actions (`Run` refuses them): stdout goes straight to the caller's writer with no cap, stderr is capped at 4 MiB and masked. A dump fails after 5 minutes without a stdout byte and is killed at a 6-hour ceiling; a failed write stops it at once. The size actions are ordinary read commands through `Run`, with the 15-second timeout.

Verified on `postgres:17-alpine` (busybox `sh`), `mysql:9` (bash), `mariadb:11` and `mongo:7` (dash), with a non-root MySQL and MariaDB user, a MongoDB root user, `*_FILE` secrets and the password `p:a\ss"w'o#r d;$x`. The stop (decision 5) was verified on the same four images: the same dump bytes and exit status as before, the same stderr for a wrong password, a stopped (`SIGSTOP`) or running orphan ended after its client was killed, a second dump refused while one runs, a forwarded `TERM`, and stale pid files cleaned.

## Consequences

**Easier:**

- Backups work with however the owner set the credentials, without the panel ever holding them.
- The whole attack surface is seven short constants (six database scripts and the stop script, with their shared parts) and one name rule, reviewed in one file.

**Harder:**

- ADR 5's "no shell" now has an exception, limited to these registry entries; any change to a script needs the same review as a change to the registry.
- The scripts depend on the official images' variable names, a POSIX `sh`, `cat`, `sed` and `mktemp` in the image, and on the clients' option-file syntax. An image without them fails at Dump with the shell's error.
- A `*_FILE` secret with an interior newline breaks its line in the option file, `.pgpass` or YAML config, so the dump fails with the client's error; nothing leaks. The official entrypoints have the same limit.
- Killing the `docker` client does not kill the process inside the container, so the panel has to stop an orphaned dump itself (decision 5). When that stop cannot run (Docker unreachable at that moment, or the panel killed without a shutdown), the orphan runs to completion inside the container, holding its `--single-transaction` snapshot or `pg_dump` locks; a retry is then refused with "A previous backup of this database is still finishing inside the container; try again in a few minutes." until it ends, instead of overlapping it. The stop needs `/proc`, `kill`, `tr`, `cat` and `sleep` with a fraction (busybox, coreutils); without `/proc` nothing is ever seen as live, so the stop does nothing and no dump is refused. A dumper of another origin that happens to reuse the recorded pid with the same name would be signalled; the shell is matched by its command line. A shell that ignores `TERM` for 2 s (stuck in a foreground command before its dumper starts) is `KILL`ed and leaves its credentials file (mode 0600) in `/tmp`. A process inside the container that can write `/tmp` can make a dump refuse (a live process named like a dumper whose pid it writes there) but cannot redirect the panel's writes or have it signal a process that is not a dump.
- MySQL 9.7's `mysqldump` run by a non-root user reports an `Error:` line on stderr about masking policies and still exits 0; the dump is complete apart from those policies.
- The size read runs `mongosh --quiet --norc --nodb --eval` non-interactively inside the database's container; mongosh's own telemetry settings in that image apply, and the panel does not change them.
