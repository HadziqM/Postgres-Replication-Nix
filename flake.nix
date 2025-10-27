{
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixpkgs-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
    systems.url = "github:nix-systems/default";
    process-compose-flake.url = "github:Platonic-Systems/process-compose-flake";
    services-flake.url = "github:juspay/services-flake";
  };
  outputs =
    inputs:
    inputs.flake-parts.lib.mkFlake { inherit inputs; } {
      systems = import inputs.systems;
      imports = [
        inputs.process-compose-flake.flakeModule
      ];
      perSystem =
        {
          pkgs,
          lib,
          ...
        }:
        let
          cfg-rep = lib.importTOML ./config.toml;
          master = cfg-rep.master;

          go-server = pkgs.buildGoModule {
            pname = "db-replication";
            version = "0.1.0";
            doCheck = false;
            src = ./.;
            vendorHash = "sha256-Z9YUqkutXv8R+G2XsO6t9v/om1lYPLOO0w5uTFphw1k=";
          };

          postgresMaster = {
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
                  schemas = [
                    ./chat.sql
                  ];
                }
              ];
            };

            #default hbaConf already allow local replication

          };

          standbySignal =
            idx:
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

                # start after database initialization but before the postgres service
                depends_on."${pgName}-init".condition = "process_completed_successfully";
              };
            };

          posgresSlave =
            idx:
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

          baseProcessCompose = no-server: {
            cli.options = {
              inherit no-server;
            };
            imports = [
              inputs.services-flake.processComposeModules.default
            ];
          };

          slaveCompose =
            idx:
            baseProcessCompose true
            // {
              services = posgresSlave idx;
              settings.processes = standbySignal idx // {

                # make postgres service run after cleaning script
                # - init -> cleaning -> service
                "postgres-slave-${toString idx}".depends_on."signal-${toString idx}".condition =
                  "process_completed_successfully";
              };
            };

          defaultCompose = baseProcessCompose true // {
            services = postgresMaster;
          };

        in
        {
          process-compose = {
            default = defaultCompose;
            master = defaultCompose;
            slave-1 = slaveCompose 0;
            slave-2 = slaveCompose 1;
            server-test = baseProcessCompose true // {
              services = {
                postgres = postgresMaster.postgres // (posgresSlave 0).postgres;
              };
              settings.processes = standbySignal 0 // {
                "pgcat" = {
                  command = ''
                    ${pkgs.pgcat}/bin/pgcat
                  '';
                  depends_on = {
                    "postgres-slave-0".condition = "process_healthy";
                  };
                };
                "go-server" = {
                  command = ''
                    ${go-server}/bin/db-replication
                  '';
                  depends_on = {
                    "pgcat".condition = "process_started";
                  };
                };
                "postgres-slave-0".depends_on."signal-0".condition = "process_completed_successfully";
                "postgres-slave-0-init".depends_on."postgres-master".condition = "process_healthy";
              };
            };
          };

          devShells.default = pkgs.mkShell {
            nativeBuildInputs = with pkgs; [
              postgresql
              gopls
              go
              pgcat
            ];
          };
        };
    };
}
