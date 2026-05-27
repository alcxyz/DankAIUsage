{ lib, buildGoModule, version ? "dev" }:

buildGoModule {
  pname = "dankaiusage";
  inherit version;

  src = ./.;

  vendorHash = null;

  subPackages = [ "cmd/dankaiusage" ];

  ldflags = [ "-s" "-w" "-X main.version=${version}" ];

  meta = with lib; {
    description = "Local Codex and Claude usage collector for DankMaterialShell";
    homepage = "https://github.com/alcxyz/DankAIUsage";
    license = licenses.mit;
    mainProgram = "dankaiusage";
  };
}
