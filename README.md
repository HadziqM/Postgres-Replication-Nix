# Postgres Streaming Replication

PostgreSQL streaming replication allows you to maintain one or more standby servers that continuously replicate changes from a primary (master) server in near real-time. This setup improves reliability, provides failover capabilities, and can scale read-heavy workloads.
This article outlines how to configure streaming replication declaratively using **Nix Flakes** and **process-compose-flake**.

---

## Overview

The idea is to define a complete replication topology in a single Nix flake that manages both the master and replica instances. Each instance (master and slaves) is described as a `process-compose` service, leveraging the deterministic and reproducible nature of Nix.
We’ll build the following setup:

* **Postgres master**
  Configured to allow replication, manage WAL segments, and create a replication role.

* **Postgres replicas (slaves)**
  Each replica initializes via `pg_basebackup` and runs in continuous recovery mode using a `standby.signal` file.

---

## Postgres Master

The **Postgres master** acts as the primary data source for all replicas. Its job is to stream Write-Ahead Logs (WAL) to connected standby databases so they can continuously replay changes in near real time.
Functionally, the master behaves like a normal PostgreSQL instance, but with additional parameters in `postgresql.conf` to enable replication and WAL shipping.

### Required WAL Configuration

1. **`wal_level = "replica"`**
   This setting controls how much information PostgreSQL writes to its WAL.
   Setting it to `"replica"` enables enough detail for streaming replication and read-only standbys to stay in sync with the primary.
   Without this level, standbys cannot receive or apply WAL changes.

2. **`max_wal_senders`**
   Defines how many WAL sender processes the master can spawn to stream data to replicas.
   Each replica connection consumes one WAL sender.
   A common rule of thumb is `3 + 2 × number_of_replicas` to leave headroom for reconnections or temporary lag.

3. **`wal_keep_size`**
   Specifies how much WAL data PostgreSQL should keep on disk before being recycled.
   This ensures that replicas can fetch any missing WAL segments even if they briefly disconnect.
   For small deployments, a few hundred megabytes is usually enough; for high-latency or high-traffic networks, increase this value accordingly.

### Required HBA Configuration

In `pg_hba.conf`, you must allow replication connections from the hosts that will act as replicas.
This is done by adding a line that permits access to the special `replication` database for the appropriate address range and authentication method.

Example for local IPv6 access:

```text
host replication all ::1/128 trust
```

For a more secure setup, use `md5` or `scram-sha-256` instead of `trust`.
Using `trust` is convenient for testing, as it allows connections without a password.

---

When replication is correctly configured, and a standby connects, the master’s log output will include entries like:

```shell
[postgres-master] 2025-10-24 15:27:22.179 GMT [5689] LOG:  checkpoint complete: wrote 3 buffers (0.0%); 0 WAL file(s) added, 0 removed, 0 recycled; write=0.001 s, sync=0.003 s, total=0.032 s; sync files=2, longest=0.002 s, average=0.002 s; distance=16105 kB, estimate=16105 kB; lsn=0/4000080, redo lsn=0/4000028
```

These messages indicate that the WAL checkpoint cycle is completing normally and that the master is generating WAL segments available for streaming.
Once a replica connects using the proper `primary_conninfo`, you will see additional logs such as `LOG:  started streaming WAL`, confirming active replication sessions.

---

## Postgres Replica (Slave)

The **Postgres replica** acts as a complete standby copy of the master database.
A common misunderstanding when first reading about streaming replication is thinking that replicas can be initialized from any database with the same schema and data — this is incorrect.
A replica must be a byte-for-byte copy of the master’s data directory and must share the same **system identifier**, or replication will fail.

### Required Settings

1. **`primary_conninfo`**
   This setting must be written into `postgresql.auto.conf`.
   It tells the replica how to connect to the master and receive WAL data.

   ```shell
   primary_conninfo = 'host=${master.host} port=${toString master.port} user=${master.user} password=${master.password}'
   ```

2. **Standby Mode**
   The replica must be in standby mode, meaning it cannot perform any write operations.
   PostgreSQL enforces this by checking for a file named `standby.signal` in the replica’s data directory.
   The presence of this empty file tells the database to stay in recovery mode and continuously apply incoming WAL changes.

3. **Data Consistency and System Identifier**
   The replica must be initialized using `pg_basebackup` directly from the master.
   Even if two databases contain the same data, they will not replicate correctly if their **system identifiers** differ.

   Example log when replication fails due to mismatched identifiers:

   ```shell
   [postgres-slave-1] 2025-10-21 17:57:01.121 GMT [356163] LOG: waiting for WAL to become available at 0/1002000
   [postgres-slave-1] 2025-10-21 17:57:06.122 GMT [356229] FATAL: database system identifier differs between the primary and standby
   [postgres-slave-1] 2025-10-21 17:57:06.122 GMT [356229] DETAIL: The primary's identifier is 7563735451457977941, the standby's identifier is 7563735495309139737.
   ```

   To avoid this, remove any existing data directory on the replica and run `pg_basebackup` before starting the service.
   This ensures both data and identifiers match the master.

---

Once the replica is configured and connected, its logs should look like this:

```shell
[postgres-slave-1] 2025-10-24 16:34:00.854 GMT [16646] LOG:  database system was interrupted; last known up at 2025-10-24 16:33:59 GMT
[postgres-slave-1] 2025-10-24 16:34:00.870 GMT [16646] LOG:  starting backup recovery with redo LSN 0/6000028, checkpoint LSN 0/6000080, on timeline ID 1
[postgres-slave-1] 2025-10-24 16:34:00.870 GMT [16646] LOG:  entering standby mode
[postgres-slave-1] 2025-10-24 16:34:00.874 GMT [16646] LOG:  redo starts at 0/6000028
[postgres-slave-1] 2025-10-24 16:34:00.876 GMT [16646] LOG:  completed backup recovery with redo LSN 0/6000028 and end LSN 0/6000120
[postgres-slave-1] 2025-10-24 16:34:00.876 GMT [16646] LOG:  consistent recovery state reached at 0/6000120
[postgres-slave-1] 2025-10-24 16:34:00.876 GMT [16643] LOG:  database system is ready to accept read-only connections
[postgres-slave-1] 2025-10-24 16:34:00.880 GMT [16647] LOG:  started streaming WAL from primary at 0/7000000 on timeline 1
```

These logs confirm the replica has entered standby mode, reached a consistent recovery point, and is now streaming WAL data from the master.

---
## Flake Structure

The flake’s `outputs` define the following:

1. **Master configuration** — Sets up the main database instance with replication enabled.
2. **Replica setup logic** — Defines how each slave clones data from the master and enters standby mode.
3. **Process composition** — Manages the dependency order between initialization, replication setup, and service startup.

The TOML configuration file (`config.toml`) provides runtime details such as hostnames, ports, users, and passwords for the replication network.

---

## Master Configuration

The master database is defined under `postgres."postgres-master"`:

```nix
postgres."postgres-master" = {
  enable = true;
  listen_addresses = master.host;
  port = master.port;

  initialScript.before = ''
    CREATE ROLE ${master.user} WITH LOGIN PASSWORD '${master.password}' SUPERUSER;
  '';

  settings = {
    wal_level = "replica";
    max_wal_senders = 5;
    wal_keep_size = "160MB";
  };

  initialDatabases = [
    {
      name = master.database;
      schemas = [ ./chat.sql ];
    }
  ];
};
```

### Key Points

* **`wal_level = "replica"`**
  Enables WAL (Write-Ahead Logging) suitable for streaming replication.

* **`max_wal_senders`**
  Defines how many replicas can connect simultaneously.

* **`wal_keep_size`**
  Ensures sufficient WAL data is retained for standby synchronization.

* **`CREATE ROLE`**
  Creates a superuser with replication privileges before database initialization.

---

## Replica Initialization

Each replica has two major components:

1. **A setup process** that cleans the data directory, copies the base backup, and writes replication configuration.
2. **The Postgres process** that starts after setup completes.

```nix
standbySignal = idx:
let
  name = "signal-${toString idx}";
  pgName = "postgres-slave-${toString idx}";
  dataDir = "./data/${pgName}";
  conf = "primary_conninfo = 'host=${master.host} port=${toString master.port} user=${master.user} password=${master.password}'";
in
{
  ${name} = {
    command = ''
      cp "${dataDir}"/postgresql.conf ./data/
      rm -rf "${dataDir}"/*

      ${pkgs.postgresql}/bin/pg_basebackup \
        -h ${master.host} \
        -p ${toString master.port} \
        -U ${master.user} \
        -D "${dataDir}" \
        -R

      touch "${dataDir}/standby.signal"
      echo "${conf}" >> "${dataDir}/postgresql.auto.conf"
      mv ./data/postgresql.conf "${dataDir}"/
    '';

    depends_on."${pgName}-init".condition = "process_completed_successfully";
  };
};
```

### Explanation

* **`pg_basebackup`**
  Clones the master’s data directory into the slave’s `dataDir`.

* **`postgresql.auto.conf`**
  Adds replication connection details dynamically.

* **Dependency management**
  The setup runs only after the initialization process completes, ensuring that the replica’s data directory is clean and synchronized.

---

## Replica Service Definition

Each replica service is generated with:

```nix
posgresSlave = idx:
let
  slave = lib.lists.elemAt cfg-rep.replica idx;
in
{
  postgres."postgres-slave-${toString idx}" = {
    enable = true;
    listen_addresses = slave.host;
    port = slave.port;
  };
};
```

The `slaveCompose` block combines the setup and Postgres service, enforcing process ordering:

```nix
slaveCompose = idx:
  baseProcessCompose false
  // {
    services = posgresSlave idx;
    settings.processes = standbySignal idx // {
      "postgres-slave-${toString idx}".depends_on."signal-${toString idx}".condition =
        "process_completed_successfully";
    };
  };
```

---

## Process Compose Configuration

`process-compose` orchestrates the full environment. You can define multiple configurations:

```nix
process-compose = {
  default = defaultCompose;
  master = defaultCompose;
  slave-1 = slaveCompose 1;
  slave-2 = slaveCompose 2;
};
```

Each one runs independently. For instance, you can launch the master with:

```bash
nix run .#master -- up
```

Then start replicas:

```bash
nix run .#slave-1 -- up
nix run .#slave-2 -- up
```



---

## Running the Setup

1. **Prepare configuration**
   Define hosts, ports, and credentials in `config.toml`.

2. **Initialize master**
   Run the master process compose target to start the primary database.

3. **Initialize replicas**
   Each replica automatically performs base backup, writes replication configs, and enters streaming mode.

4. **Verify replication**
   Connect to the master and check replication status:

   ```sql
   SELECT * FROM pg_stat_replication;
   ```

   On replicas, confirm recovery mode:

   ```sql
   SELECT pg_is_in_recovery();
   ```

