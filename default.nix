{ lib, buildGoModule, version ? "dev", revision ? null }:

let
  # Flake-less consumers do not carry Git metadata. Fingerprint only public
  # source files so their reports still distinguish builds without local paths.
  sourceFiles = lib.filesystem.listFilesRecursive (lib.cleanSource ./.);
  fingerprintFiles = builtins.filter (path:
    let name = baseNameOf path;
    in lib.hasSuffix ".go" name || lib.hasSuffix ".qml" name
      || name == "plugin.json" || name == "go.mod"
  ) sourceFiles;
  sourceRevision = "source-" + builtins.hashString "sha256"
    (lib.concatMapStrings (path: builtins.hashFile "sha256" path) fingerprintFiles);
in

buildGoModule {
  pname = "dankaiusage";
  inherit version;

  src = ./.;

  vendorHash = null;

  subPackages = [ "cmd/dankaiusage" ];

  ldflags = [ "-s" "-w" "-X main.version=${version}" "-X main.revision=${if revision == null then sourceRevision else revision}" ];

  meta = with lib; {
    description = "Local Codex and Claude usage collector for DankMaterialShell";
    homepage = "https://github.com/alcxyz/DankAIUsage";
    license = licenses.mit;
    mainProgram = "dankaiusage";
  };
}
