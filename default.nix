{ lib, buildGoModule, python3
, version ? (builtins.fromJSON (builtins.readFile ./plugin.json)).version
, revision ? null, release ? false
}:

let
  metadata = import ./build-metadata.nix { inherit lib version revision release; };
  source = metadata.source;
  buildRevision = metadata.revision;
  buildVersion = metadata.version;
in
buildGoModule {
  pname = "dankaiusage";
  version = buildVersion;

  src = source;

  vendorHash = null;

  subPackages = [ "cmd/dankaiusage" ];

  ldflags = [ "-s" "-w" "-X main.version=${buildVersion}" "-X main.revision=${buildRevision}" ];
  nativeBuildInputs = [ python3 ];
  postInstall = ''
    python3 scripts/package.py --stage-only --output "$out" \
      --revision ${lib.escapeShellArg buildRevision} ${lib.optionalString release "--release"}
    test "$($out/bin/dankaiusage version)" = ${lib.escapeShellArg buildVersion}
  '';

  meta = with lib; {
    description = "Local Codex and Claude usage collector for DankMaterialShell";
    homepage = "https://github.com/alcxyz/DankAIUsage";
    license = licenses.mit;
    mainProgram = "dankaiusage";
  };
}
