{ lib, buildGoModule, python3
, version ? (builtins.fromJSON (builtins.readFile ./plugin.json)).version
, revision ? null, release ? false
}:

let
  # Flake-less consumers do not carry Git metadata. Fingerprint only public
  # source files so their reports still distinguish builds without local paths.
  source = lib.cleanSourceWith {
    src = ./.;
    filter = path: type:
      lib.cleanSourceFilter path type
      && !(builtins.elem (baseNameOf path) [ "dist" "__pycache__" ".envrc" ]);
  };
  sourceFiles = lib.filesystem.listFilesRecursive source;
  fingerprintFiles = builtins.filter (path:
    let name = baseNameOf path;
    in lib.hasSuffix ".go" name || lib.hasSuffix ".qml" name
      || lib.hasSuffix ".nix" name || lib.hasSuffix ".py" name
      || name == "plugin.json" || name == "go.mod"
  ) sourceFiles;
  sourceRevision = "source-" + builtins.hashString "sha256"
    (lib.concatMapStrings (path: builtins.hashFile "sha256" path) fingerprintFiles);
  buildRevision = if revision == null then sourceRevision else revision;
  buildVersion = import ./build-version.nix {
    inherit version release;
    revision = buildRevision;
  };
in
assert version == (builtins.fromJSON (builtins.readFile ./plugin.json)).version;
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
