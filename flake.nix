{
  description = "hanko CLI dev environment";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };
  outputs =
    {
      self,
      nixpkgs,
      gomod2nix,
      ...
    }@inputs:
    let
      supportedSystems = [
        "aarch64-darwin"
        "arm64-darwin"
        "x86_64-darwin"
        "x86_64-linux"
      ];
      eachSystem = f: nixpkgs.lib.genAttrs supportedSystems (system: f (pkgsFor system));
      pkgsFor =
        system:
        let
          pkgs = import nixpkgs {
            inherit system;
            config.allowUnfree = true;
          };
        in
        pkgs.extend (
          nixpkgs.lib.composeManyExtensions [
            gomod2nix.overlays.default
            hankoOverlay
          ]
        );
      treefmtEval = eachSystem (pkgs: inputs.treefmt-nix.lib.evalModule pkgs ./treefmt.nix);
      # Stamped by hanko; do not hand-edit (use `just release`).
      # Hoisted out of the overlay so it's a clear single source of truth even
      # though hanko currently only exposes one derivation — matches the D-015
      # shared-`let` pattern recommended for consumers.
      version = "0.1.1";
      commonLdflags = [
        "-s"
        "-w"
        "-X"
        "main.version=${version}"
        "-X"
        "main.commit=${self.rev or self.dirtyRev or "unknown"}"
        "-X"
        "main.date=${self.lastModifiedDate or "unknown"}"
      ];
      hankoOverlay = final: _prev: {
        hanko = final.buildGoApplication {
          pname = "hanko";
          inherit version;
          src = ./.;
          modules = ./gomod2nix.toml;
          nativeBuildInputs = [ final.installShellFiles ];
          nativeCheckInputs = [ final.git ];
          ldflags = commonLdflags;
          postInstall = ''
            installShellCompletion --cmd hanko \
              --bash <($out/bin/hanko completion bash) \
              --zsh <($out/bin/hanko completion zsh) \
              --fish <($out/bin/hanko completion fish)
          '';
        };
      };
    in
    {

      overlays.default = nixpkgs.lib.composeManyExtensions [
        gomod2nix.overlays.default
        hankoOverlay
      ];

      packages = eachSystem (pkgs: {
        default = pkgs.hanko;
      });

      formatter = eachSystem (pkgs: treefmtEval.${pkgs.stdenv.hostPlatform.system}.config.build.wrapper);
      checks = eachSystem (pkgs: {
        formatting = treefmtEval.${pkgs.stdenv.hostPlatform.system}.config.build.check self;
        smoke =
          pkgs.runCommand "hanko-smoke"
            {
              nativeBuildInputs = [ pkgs.git ];
            }
            ''
              export HOME=$TMPDIR
              bash ${./test/smoke/smoke.sh} ${pkgs.hanko}/bin/hanko
              touch $out
            '';
      });

      devShells = eachSystem (pkgs: {
        default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            gotools
            golangci-lint
            go-tools
            gopls
            gomod2nix.packages.${pkgs.stdenv.hostPlatform.system}.default
            # Hanko-on-itself: lets `just release` invoke `hanko stamp nix`
            # without `go run .`. If hanko's source breaks the build, fall
            # back to `nix develop --command go run . ...` from outside the
            # devshell.
            pkgs.hanko
          ];
          shellHook = ''
            echo "hanko devshell"
          '';
        };
      });
    };
}
