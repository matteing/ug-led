{
  description = "Go userspace LED control for the Intel UGREEN DXP2800";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-darwin"
        "x86_64-darwin"
      ];
      forAll = nixpkgs.lib.genAttrs systems;
      pkgsFor = system: import nixpkgs { inherit system; };
      moduleTest = nixpkgs.lib.nixosSystem {
        system = "x86_64-linux";
        modules = [
          self.nixosModules.default
          ({ ... }: {
            system.stateVersion = "26.05";
            boot.loader.grub.enable = false;
            fileSystems."/" = {
              device = "/dev/disk/by-label/test";
              fsType = "ext4";
            };
            services.ug-led = {
              enable = true;
              settings = {
                interface = "eth0";
                health = {
                  file = "/run/ug-led/health.json";
                  preset = "enhanced";
                  max_age_seconds = 90;
                  critical_brightness = 24;
                  monitored = [
                    "power"
                    "disk1"
                    "disk2"
                  ];
                };
              };
              collector = {
                enable = true;
                settings = builtins.fromJSON (builtins.readFile ./examples/health-collector.json);
              };
            };
          })
        ];
      };
    in
    {
      packages = forAll (
        system:
        let
          pkgs = pkgsFor system;
        in
        {
          default = pkgs.callPackage ./nix/package.nix { };
        }
      );
      formatter = forAll (
        system:
        let
          pkgs = pkgsFor system;
        in
        pkgs.writeShellApplication {
          name = "ug-led-fmt";
          text = ''
            test -f flake.nix || { echo "Run nix fmt from the project root" >&2; exit 1; }
            exec ${pkgs.nixfmt-tree}/bin/treefmt --tree-root "$PWD" "$@"
          '';
        }
      );
      devShells = forAll (
        system:
        let
          pkgs = pkgsFor system;
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.nixfmt-tree
            ];
          };
        }
      );
      nixosModules.default = import ./nix/module.nix;
      checks = forAll (
        system:
        let
          pkgs = pkgsFor system;
        in
        {
          package = self.packages.${system}.default;
          collector-config = pkgs.runCommand "ug-led-collector-config-check" { } ''
            ${
              self.packages.${system}.default
            }/bin/ug-led collect --config ${./examples/health-collector.json} --check > $out
          '';
          config = pkgs.runCommand "ug-led-config-check" { } ''
            ${self.packages.${system}.default}/bin/ug-led check --config ${./examples/dxp2800.json} > $out
          '';
        }
        // nixpkgs.lib.optionalAttrs (system == "x86_64-linux") {
          nixos-module =
            assert moduleTest.config.systemd.services.ug-led.serviceConfig.Restart == "no";
            assert moduleTest.config.systemd.services.ug-led-health.serviceConfig.DevicePolicy == "closed";
            assert moduleTest.config.systemd.services.ug-led-health.serviceConfig.DynamicUser;
            assert builtins.isString moduleTest.config.system.build.toplevel.drvPath;
            pkgs.runCommand "ug-led-module-check" { } ''
              ${self.packages.${system}.default}/bin/ug-led check --config ${
                moduleTest.config.environment.etc."ug-led/config.json".source
              } > $out
            '';
        }
      );
    };
}
