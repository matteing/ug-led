{ lib, buildGoModule }:
buildGoModule {
  pname = "ug-led";
  version = "0.2.2";
  src = lib.cleanSource ../.;
  vendorHash = null;
  subPackages = [ "cmd/ug-led" ];
  env.CGO_ENABLED = 0;
  ldflags = [
    "-s"
    "-w"
    "-X main.version=0.2.2"
  ];
  meta = {
    description = "Conservative userspace front-panel LED control for the Intel UGREEN DXP2800";
    homepage = "https://github.com/matteing/ug-led";
    license = lib.licenses.mit;
    mainProgram = "ug-led";
    platforms = [
      "x86_64-linux"
      "aarch64-darwin"
      "x86_64-darwin"
    ];
  };
}
