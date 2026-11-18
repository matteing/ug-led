{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.ug-led;
  format = pkgs.formats.json { };
  configFile = format.generate "ug-led.json" cfg.settings;
in
{
  options.services.ug-led = {
    enable = lib.mkEnableOption "UGREEN DXP2800 front-panel LED control";
    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.callPackage ./package.nix { };
      description = "The ug-led package to run.";
    };
    settings = lib.mkOption {
      type = format.type;
      default = { };
      example = {
        interface = "enp2s0";
        bays = [
          "ata1"
          "ata2"
        ];
        poll_ms = 200;
        night = {
          start = "22:00";
          end = "07:00";
          timezone = "Etc/UTC";
          brightness = 16;
        };
      };
      description = "JSON settings; an explicit network interface is required unless its LED mode is off. See the project README.";
    };
  };
  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = pkgs.stdenv.hostPlatform.system == "x86_64-linux";
        message = "ug-led hardware access supports only the original Intel DXP2800 (x86_64-linux).";
      }
    ];
    boot.kernelModules = [
      "i2c-i801"
      "i2c-dev"
    ];
    environment.systemPackages = [ cfg.package ];
    environment.etc."ug-led/config.json".source = configFile;
    systemd.services.ug-led = {
      description = "UGREEN DXP2800 front-panel LEDs";
      wantedBy = [ "multi-user.target" ];
      after = [ "systemd-modules-load.service" ];
      serviceConfig = {
        Type = "simple";
        ExecStartPre = "${lib.getExe cfg.package} check --config ${configFile}";
        ExecStart = "${lib.getExe cfg.package} run --config ${configFile}";
        # Deliberately stop on failure; no repeated automatic hardware writes.
        Restart = "no";
        TimeoutStopSec = 5;
        User = "root";
        UMask = "0077";
        NoNewPrivileges = true;
        CapabilityBoundingSet = "";
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectKernelLogs = true;
        ProtectControlGroups = true;
        ProtectClock = true;
        RestrictSUIDSGID = true;
        RestrictRealtime = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        RestrictAddressFamilies = [ "AF_UNIX" ];
        # The bus number is discovered, not fixed. The program further restricts
        # access to the verified Intel adapter and slave address 0x3a.
        DevicePolicy = "closed";
        DeviceAllow = [ "char-i2c rw" ];
        SystemCallArchitectures = "native";
        SystemCallFilter = [
          "@system-service"
          "~@mount"
          "~@reboot"
          "~@swap"
        ];
      };
    };
  };
}
