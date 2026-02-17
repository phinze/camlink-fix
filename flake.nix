{
  description = "camlink-fix - auto-reset Elgato Cam Link 4K after macOS sleep";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        camlink-fix = pkgs.buildGoModule {
          pname = "camlink-fix";
          version = "0.1.0-${builtins.substring 0 12 (self.lastModifiedDate or "19700101000000")}";

          src = builtins.path {
            path = ./.;
            name = "camlink-fix-source";
          };

          vendorHash = "sha256-B3x0I+vTRX6c5Lr0LSmccEEXswjloBY84B38imyScoo=";

          # mac-sleep-notifier uses cgo
          env.CGO_ENABLED = "1";

          meta = with pkgs.lib; {
            description = "Auto-reset Elgato Cam Link 4K after macOS sleep/wake";
            homepage = "https://github.com/phinze/camlink-fix";
            license = licenses.mit;
            mainProgram = "camlink-fix";
          };
        };
      in
      {
        packages = {
          default = camlink-fix;
          camlink-fix = camlink-fix;
        };

        apps.default = flake-utils.lib.mkApp {
          drv = camlink-fix;
        };

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            gopls
            gotools
            go-tools
            delve
          ];

          shellHook = ''
            export GOPATH="$PWD/.go"
            export PATH="$GOPATH/bin:$PATH"
          '';
        };
      }
    )
    // {
      darwinModules.default = import ./nix/module.nix self;
    };
}
